package ratelimit

import (
	"context"
	"strconv"
	"sync"
	"time"
)

// fixedWindowScript is one atomic Redis step: INCR the window counter and, only on
// its birth (n == 1), arm a one-shot TTL. The window epoch is already folded into
// KEYS[1] client-side, so a new window is simply a new key starting at 0 — LIMITING
// is always correct regardless of the TTL. PEXPIRE is only best-effort eviction of
// the dead epoch key; if it ever fails to take (e.g. a server crash mid-script) the
// stale key merely wastes memory until Redis maxmemory eviction, never mis-limits.
const fixedWindowScript = `local n = redis.call('INCR', KEYS[1])
if n == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return n`

const defaultRedisPrefix = "parapet:rl:"

// RedisRunner is the minimal Redis surface the distributed limiter needs: run a Lua
// script and return its integer reply. Inject an adapter over your client (go-redis,
// rueidis, redigo, …) so pkg/ratelimit hard-depends on no Redis client. keys/args
// map to the script's KEYS/ARGV. Implementations MUST be safe for concurrent use and
// MUST return either the script's integer reply or a non-nil error (a broken adapter
// that returns (0, nil) would be read as "1 request seen" and admit).
type RedisRunner interface {
	Eval(ctx context.Context, script string, keys []string, args ...any) (int64, error)
}

// RedisRunnerFunc adapts a plain func to RedisRunner.
type RedisRunnerFunc func(ctx context.Context, script string, keys []string, args ...any) (int64, error)

// Eval implements RedisRunner.
func (f RedisRunnerFunc) Eval(ctx context.Context, script string, keys []string, args ...any) (int64, error) {
	return f(ctx, script, keys, args...)
}

// RedisFixedWindow creates a Redis-backed fixed-window rate limiter, so N proxy
// instances enforce one GLOBAL limit. runner is your injected Redis client adapter.
// It fails OPEN on a Redis error (availability over strict limiting); build the
// strategy directly to change that.
func RedisFixedWindow(runner RedisRunner, rate int, unit time.Duration) *RateLimiter {
	return New(&RedisFixedWindowStrategy{Runner: runner, Max: rate, Size: unit, FailOpen: true})
}

// RedisFixedWindowPerSecond creates a Redis-backed fixed-window limiter per second.
func RedisFixedWindowPerSecond(runner RedisRunner, rate int) *RateLimiter {
	return RedisFixedWindow(runner, rate, time.Second)
}

// RedisFixedWindowPerMinute creates a Redis-backed fixed-window limiter per minute.
func RedisFixedWindowPerMinute(runner RedisRunner, rate int) *RateLimiter {
	return RedisFixedWindow(runner, rate, time.Minute)
}

// RedisFixedWindowPerHour creates a Redis-backed fixed-window limiter per hour.
func RedisFixedWindowPerHour(runner RedisRunner, rate int) *RateLimiter {
	return RedisFixedWindow(runner, rate, time.Hour)
}

// RedisFixedWindowStrategy implements Strategy with a Redis-backed fixed window, so a
// fleet of proxy instances shares one global counter per key. The hot path is one
// atomic Lua round-trip; all mutable state lives in Redis (no local map, no mutex).
//
// Config is read once on first use; set fields before serving. The zero value fails
// CLOSED on a Redis error (deny); the constructors set FailOpen = true (fail open for
// availability). Like the in-memory FixedWindowStrategy it admits up to 2*Max across
// a window boundary, ignores Weight, and has no background goroutine.
//
//nolint:govet // fields grouped by role (state, then config) for readability
type RedisFixedWindowStrategy struct {
	once    sync.Once
	size    time.Duration // resolved Size
	prefix  string        // resolved Prefix
	timeout time.Duration // resolved Timeout

	// Runner is the REQUIRED injected Redis client adapter. A nil Runner makes every
	// Take fail per FailOpen (no panic on the hot path).
	Runner RedisRunner

	// Max is the global tokens per window. Max <= 0 admits nothing.
	Max int

	// Size is the window size; <= 0 resolves to time.Second. Windows that do not
	// divide an hour are fine here (After is epoch-anchored, see below).
	Size time.Duration

	// Prefix namespaces the Redis keys; "" resolves to "parapet:rl:".
	Prefix string

	// Timeout bounds each Take's Redis round-trip (a per-call context deadline); <= 0
	// resolves to 100ms. It bounds a SLOW (not just failed) Redis off the hot path.
	Timeout time.Duration

	// FailOpen decides a Redis error: true admits (the constructors' default), false
	// denies (the zero value). Fail open trades strict limiting for availability.
	FailOpen bool

	// OnError observes a Redis error (timeout, dial, script error); nil ignores it.
	OnError func(error)
}

func (b *RedisFixedWindowStrategy) init() {
	b.size = b.Size
	if b.size <= 0 {
		b.size = time.Second
	}
	b.timeout = b.Timeout
	if b.timeout <= 0 {
		b.timeout = 100 * time.Millisecond
	}
	b.prefix = b.Prefix
	if b.prefix == "" {
		b.prefix = defaultRedisPrefix
	}
}

// Take atomically increments the current window's Redis counter and reports whether
// the caller is within Max. Exactly one round-trip; the over-Max request is still
// counted (the safe direction — never over-admits). On any Redis error (including a
// Timeout) it fails open or closed per FailOpen.
func (b *RedisFixedWindowStrategy) Take(key string) bool {
	b.once.Do(b.init)
	if b.Runner == nil {
		return b.FailOpen
	}

	// Client-side epoch -> the same wall-clock window as the in-memory FixedWindow and
	// as After below. A distinct epoch is a distinct key, so window rollover is
	// lock-free (a fresh key starts at 0).
	epoch := time.Now().UnixNano() / int64(b.size)
	rkey := b.prefix + key + ":" + strconv.FormatInt(epoch, 10)
	// Window + 1s slack so a clock-skewed reader of the SAME epoch still finds the
	// key before it self-evicts; PEXPIRE is just a janitor. The slack also floors the
	// TTL at >= 1s, so a sub-second Size never yields a non-positive PEXPIRE.
	ttlMs := b.size.Milliseconds() + 1000

	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()

	n, err := b.Runner.Eval(ctx, fixedWindowScript, []string{rkey}, ttlMs)
	if err != nil {
		if b.OnError != nil {
			b.OnError(err)
		}
		return b.FailOpen
	}
	return n <= int64(b.Max)
}

// Put is a no-op: a fixed window does not return tokens (matches FixedWindow).
func (b *RedisFixedWindowStrategy) Put(string) {}

// After returns the time until the current window rolls over, with NO round-trip. It
// is anchored to the SAME integer epoch (now/Size) folded into the Redis key, so the
// returned duration is exactly the time to the end of the current window's key.
//
// This is the same epoch grid FixedWindowStrategy.{Take,After} use, and it is
// correct for ANY Size (unlike time.Truncate's year-1 anchoring, which only matches
// for sizes that divide the year1->epoch offset). The middleware only calls After
// after a rejecting Take, so it always reflects a window the key is currently in.
func (b *RedisFixedWindowStrategy) After(string) time.Duration {
	b.once.Do(b.init) // resolve b.size even if After races ahead of the first Take
	now := time.Now()
	epoch := now.UnixNano() / int64(b.size)
	next := time.Unix(0, (epoch+1)*int64(b.size))
	return next.Sub(now)
}
