package ratelimit_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/moonrhythm/parapet/pkg/ratelimit"
)

// fakeRedis simulates a single-threaded Redis INCR+PEXPIRE over an in-process map,
// capturing the last call so tests can assert the key/args the strategy builds.
type fakeRedis struct {
	mu       sync.Mutex
	data     map[string]int64
	lastKeys []string
	lastArgs []any
	err      error         // if set, Eval returns it
	block    chan struct{} // if set, Eval blocks until ctx is done (slow-Redis sim)
	calls    atomic.Int64
}

func (f *fakeRedis) Eval(ctx context.Context, _ string, keys []string, args ...any) (int64, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.lastKeys, f.lastArgs = keys, args
	f.mu.Unlock()

	if f.block != nil {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-f.block:
		}
	}
	if f.err != nil {
		return 0, f.err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.data == nil {
		f.data = map[string]int64{}
	}
	f.data[keys[0]]++
	return f.data[keys[0]], nil
}

func newFakeRedis() *fakeRedis { return &fakeRedis{data: map[string]int64{}} }

func TestRedisFixedWindowConstructors(t *testing.T) {
	t.Parallel()
	f := newFakeRedis()

	cases := []struct {
		name string
		m    *RateLimiter
		size time.Duration
	}{
		{"RedisFixedWindow", RedisFixedWindow(f, 5, 2*time.Second), 2 * time.Second},
		{"PerSecond", RedisFixedWindowPerSecond(f, 5), time.Second},
		{"PerMinute", RedisFixedWindowPerMinute(f, 5), time.Minute},
		{"PerHour", RedisFixedWindowPerHour(f, 5), time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ok := tc.m.Strategy.(*RedisFixedWindowStrategy)
			require.True(t, ok)
			assert.True(t, s.FailOpen, "constructors fail open")
			assert.Equal(t, 5, s.Max)
			assert.Equal(t, tc.size, s.Size)
		})
	}
}

func TestRedisFixedWindowAdmitsThenDenies(t *testing.T) {
	t.Parallel()
	f := newFakeRedis()
	b := &RedisFixedWindowStrategy{Runner: f, Max: 2, Size: time.Hour}

	assert.True(t, b.Take("a"))
	assert.True(t, b.Take("a"))
	assert.False(t, b.Take("a"), "3rd over Max=2 denied")

	// The key is namespaced and epoch-suffixed. Take reads the clock internally, so
	// bracket it with both candidate epochs: if the top of the hour falls between
	// Take's read and ours, the suffix is e1 or e2 (e2-e1 <= 1 for any stall under an
	// hour) — a single post-hoc epoch read raced that crossing.
	e1 := time.Now().UnixNano() / int64(time.Hour)
	assert.True(t, b.Take("b"), "a different key is independent")
	e2 := time.Now().UnixNano() / int64(time.Hour)

	require.Len(t, f.lastKeys, 1)
	assert.True(t, strings.HasPrefix(f.lastKeys[0], "parapet:rl:"), "default prefix")
	assert.True(t,
		strings.HasSuffix(f.lastKeys[0], ":"+strconv.FormatInt(e1, 10)) ||
			strings.HasSuffix(f.lastKeys[0], ":"+strconv.FormatInt(e2, 10)),
		"epoch suffix (either side of a possible hour crossing)")
	assert.Contains(t, f.lastKeys[0], "b")
}

// TestRedisFixedWindowExactlyMaxUnderRace is THE cross-instance no-double-grant
// property, simulated: many goroutines hammer one key against a single-threaded
// (mutex-serialized) counter; exactly Max must be admitted.
func TestRedisFixedWindowExactlyMaxUnderRace(t *testing.T) {
	t.Parallel()
	const g, r, max = 16, 50, 100
	f := newFakeRedis()
	b := &RedisFixedWindowStrategy{Runner: f, Max: max, Size: time.Hour, FailOpen: true}

	var admitted atomic.Int64
	var wg sync.WaitGroup
	for range g {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range r {
				if b.Take("k") {
					admitted.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	assert.EqualValues(t, max, admitted.Load(), "exactly Max admitted across all goroutines")
}

func TestRedisFixedWindowRollsToFreshWindow(t *testing.T) {
	t.Parallel()
	f := newFakeRedis()
	const size = 50 * time.Millisecond
	b := &RedisFixedWindowStrategy{Runner: f, Max: 1, Size: size}

	// Epochs are absolute now/Size and Take reads the clock internally, so a scheduler
	// stall between the two Takes can push the second into a fresh epoch (which admits
	// and breaks the exhaustion assert). Bracket the pair with epoch reads — e1 == e2
	// proves both internal reads shared one window — and retry on a straddle: each
	// attempt re-aligns to the NEXT boundary, so its epoch key is always virgin (a
	// straddled prior attempt only ever incremented strictly earlier epochs).
	var ok1, ok2, sameEpoch bool
	for range 5 {
		now := time.Now().UnixNano()
		time.Sleep(time.Duration((now/int64(size)+1)*int64(size) - now))
		e1 := time.Now().UnixNano() / int64(size)
		ok1 = b.Take("k")
		ok2 = b.Take("k")
		if e2 := time.Now().UnixNano() / int64(size); e1 == e2 {
			sameEpoch = true
			break
		}
	}
	require.True(t, sameEpoch, "could not land both Takes in one 50ms epoch after 5 attempts")
	require.True(t, ok1)
	require.False(t, ok2, "window exhausted")
	time.Sleep(size) // cross into the next epoch (one-sided safe: any later epoch is fresh)
	assert.True(t, b.Take("k"), "a fresh epoch key starts at 0")
}

// TestRedisFixedWindowMaxZeroAdmitsNothing pins the Max<=0 branch (where n<=Max goes
// negative), matching the in-memory FixedWindow's admit-nothing reading.
func TestRedisFixedWindowMaxZeroAdmitsNothing(t *testing.T) {
	t.Parallel()
	b := &RedisFixedWindowStrategy{Runner: newFakeRedis(), Max: 0, Size: time.Hour}
	assert.False(t, b.Take("k"))
	assert.False(t, b.Take("k"))
}

// TestRedisFixedWindowAfterEpochAnchored: After must equal the time to the end of the
// integer-epoch window for ANY Size — including windows that do NOT divide an hour,
// where the in-memory FixedWindow's time.Truncate answer would be wrong.
func TestRedisFixedWindowAfterEpochAnchored(t *testing.T) {
	t.Parallel()
	for _, size := range []time.Duration{time.Second, 7 * time.Second, 13 * time.Minute, time.Hour} {
		b := &RedisFixedWindowStrategy{Runner: newFakeRedis(), Max: 1, Size: size}
		before := time.Now()
		got := b.After("k")
		after := time.Now()
		// After's internal clock read lies in [before, after]. If both brackets share
		// an epoch, the internal read does too, and since After is strictly decreasing
		// within an epoch, got must land in [end-after, end-before] — bounds that hold
		// under ARBITRARY stalls, unlike the old fixed 5ms delta off a single pre-call
		// read (the want-got gap there was exactly the inter-read stall). Skip only the
		// vanishing boundary-crossing case.
		if before.UnixNano()/int64(size) != after.UnixNano()/int64(size) {
			continue
		}
		end := time.Unix(0, (before.UnixNano()/int64(size)+1)*int64(size))
		assert.LessOrEqual(t, got, end.Sub(before),
			"After is anchored to the integer epoch boundary for Size=%s", size)
		assert.GreaterOrEqual(t, got, end.Sub(after),
			"After is anchored to the integer epoch boundary for Size=%s", size)
		assert.Positive(t, got)
		assert.LessOrEqual(t, got, size)
	}
}

func TestRedisFixedWindowFailOpenClosed(t *testing.T) {
	t.Parallel()
	boom := errors.New("redis down")

	t.Run("fail open admits and reports", func(t *testing.T) {
		var gotErr error
		var n atomic.Int64
		f := &fakeRedis{err: boom}
		b := &RedisFixedWindowStrategy{Runner: f, Max: 1, Size: time.Second, FailOpen: true,
			OnError: func(err error) { gotErr = err; n.Add(1) }}
		assert.True(t, b.Take("k"))
		assert.Equal(t, boom, gotErr)
		assert.EqualValues(t, 1, n.Load(), "OnError fired exactly once")
	})

	t.Run("fail closed denies", func(t *testing.T) {
		b := &RedisFixedWindowStrategy{Runner: &fakeRedis{err: boom}, Max: 1, Size: time.Second}
		assert.False(t, b.Take("k"), "zero value fails closed")
	})
}

// TestRedisFixedWindowTimeoutBounded: a slow Redis must be bounded by Timeout off the
// hot path (the failure mode an adapter-side timeout cannot guarantee).
func TestRedisFixedWindowTimeoutBounded(t *testing.T) {
	t.Parallel()
	f := &fakeRedis{block: make(chan struct{})} // never unblocks: only ctx cancels it
	b := &RedisFixedWindowStrategy{Runner: f, Max: 1, Size: time.Second, Timeout: 20 * time.Millisecond, FailOpen: true}

	start := time.Now()
	got := b.Take("k")
	elapsed := time.Since(start)
	assert.True(t, got, "fails open after the deadline")
	assert.Less(t, elapsed, 500*time.Millisecond, "the per-Take Timeout bounds the stall")
	assert.GreaterOrEqual(t, elapsed, 15*time.Millisecond, "it actually waited out the deadline")
}

func TestRedisFixedWindowNilRunner(t *testing.T) {
	t.Parallel()
	assert.NotPanics(t, func() {
		assert.False(t, (&RedisFixedWindowStrategy{Max: 1, Size: time.Second}).Take("k"), "nil runner fails closed by default")
		assert.True(t, (&RedisFixedWindowStrategy{Max: 1, Size: time.Second, FailOpen: true}).Take("k"))
	})
}

// TestRedisFixedWindowTTLFloor: the +1s slack keeps PEXPIRE >= 1s even for a
// sub-millisecond Size (a raw ms TTL would be 0/negative).
func TestRedisFixedWindowTTLFloor(t *testing.T) {
	t.Parallel()
	f := newFakeRedis()
	b := &RedisFixedWindowStrategy{Runner: f, Max: 1, Size: 500 * time.Microsecond}
	b.Take("k")
	require.Len(t, f.lastArgs, 1)
	assert.GreaterOrEqual(t, f.lastArgs[0].(int64), int64(1000), "TTL floored at >= 1000ms")
}

func TestRedisFixedWindowRunnerFuncForwards(t *testing.T) {
	t.Parallel()
	var gotKeys []string
	var gotArgs []any
	fn := RedisRunnerFunc(func(_ context.Context, _ string, keys []string, args ...any) (int64, error) {
		gotKeys, gotArgs = keys, args
		return 1, nil
	})
	b := &RedisFixedWindowStrategy{Runner: fn, Max: 1, Size: time.Hour}
	assert.True(t, b.Take("k"))
	require.Len(t, gotKeys, 1)
	assert.True(t, strings.HasPrefix(gotKeys[0], "parapet:rl:"))
	require.Len(t, gotArgs, 1)
	assert.EqualValues(t, int64(time.Hour.Milliseconds()+1000), gotArgs[0])
}

// TestRedisFixedWindowServeHandler wires the strategy through the middleware: the
// second request from one client gets a 429 with Retry-After, and Put is never used.
func TestRedisFixedWindowServeHandler(t *testing.T) {
	t.Parallel()
	m := RedisFixedWindowPerMinute(newFakeRedis(), 1)
	m.Key = func(*http.Request) string { return "client" }
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, httptest.NewRequest("GET", "/", nil))
	assert.Equal(t, http.StatusOK, w1.Code)

	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, httptest.NewRequest("GET", "/", nil))
	assert.Equal(t, http.StatusTooManyRequests, w2.Code)
	assert.NotEmpty(t, w2.Header().Get("Retry-After"), "a denied request carries Retry-After")
}

// TestRedisFixedWindowAfterRacesTake: After must resolve config via once.Do even when
// it races the first Take (no data race on b.size).
func TestRedisFixedWindowAfterRacesTake(t *testing.T) {
	t.Parallel()
	b := &RedisFixedWindowStrategy{Runner: newFakeRedis(), Max: 1, Size: time.Second}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(2)
		go func() { defer wg.Done(); b.Take("k") }()
		go func() { defer wg.Done(); _ = b.After("k") }()
	}
	wg.Wait()
}
