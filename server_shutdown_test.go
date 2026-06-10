package parapet_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet"
)

// TestRegisterOnShutdownRaceHammer is the headline -race regression.
//
// Race guarded: append-vs-range. RegisterOnShutdown does
// `s.onShutdown = append(s.onShutdown, f)` (a slice-header write) while
// Shutdown does `for _, f := range s.onShutdown` (a concurrent slice-header
// read). In production RegisterOnShutdown fires lazily from request goroutines
// (pkg/healthz/healthz.go once.Do, pkg/upstream/healthcheck.go lazyOnce.Do),
// which can land exactly while a SIGTERM-driven Shutdown is iterating.
//
// Why it fails without the fix: on the unguarded code the race detector reports
// a DATA RACE between the append at RegisterOnShutdown and the range in
// Shutdown (and between two concurrent appends). The 100x200 fan-out makes
// detection effectively deterministic. With the fix both go through
// muShutdown, so the read/writes are serialized and -race is clean.
//
// No socket is bound: the bug is purely on the onShutdown slice, and Shutdown
// on a never-served http.Server returns nil instantly, so this is fast and
// non-flaky. We deliberately do NOT assert an exact fired count — the snapshot
// semantics legitimately route some late registrations through the run-now
// path; the contract (TestRegisterAfterShutdownRunsImmediately) covers that
// they still all fire. Here we assert only race-freedom and no callback loss
// (fired == registered, because run-now guarantees every callback runs).
func TestRegisterOnShutdownRaceHammer(t *testing.T) {
	t.Parallel()

	const goroutines = 100

	for iter := 0; iter < 200; iter++ {
		srv := &Server{} // GraceTimeout==0, WaitBeforeShutdown==0: Shutdown is instant.

		var fired int32
		var wg sync.WaitGroup
		start := make(chan struct{})

		// N registrants, all released together to maximize overlap with Shutdown.
		wg.Add(goroutines)
		for i := 0; i < goroutines; i++ {
			go func() {
				defer wg.Done()
				<-start
				// Each registered callback runs exactly once: either launched by
				// Shutdown's snapshot, or by RegisterOnShutdown's run-now path.
				srv.RegisterOnShutdown(func() { atomic.AddInt32(&fired, 1) })
			}()
		}

		// One Shutdown racing all the registrations.
		var shutWG sync.WaitGroup
		shutWG.Add(1)
		go func() {
			defer shutWG.Done()
			<-start
			assert.NoError(t, srv.Shutdown())
		}()

		close(start)
		wg.Wait()     // every RegisterOnShutdown returned
		shutWG.Wait() // Shutdown returned

		// Every callback fires exactly once (no lost registration): callbacks
		// launched by go f() may still be in flight, so wait for the count to
		// settle on observable state rather than sleep-and-hope.
		waitForInt32(t, &fired, goroutines, time.Second,
			"all registered callbacks must fire (snapshot or run-now), none dropped")
	}
}

// TestRegisterConcurrentAppends isolates the append-vs-append leg of the bug:
// two lazy registrants (mirroring healthz's first request vs ActiveHealthCheck's
// first request) calling RegisterOnShutdown concurrently with no Shutdown.
//
// Why it fails without the fix: two concurrent `append(s.onShutdown, f)` on the
// same slice header is a data race; -race reports it. It can also silently lose
// a registration (torn slice-header update). With the fix both appends are
// serialized under muShutdown, so all N land and -race is clean.
//
// Sensitivity: assert exactly N callbacks fire after a single Shutdown — proves
// no append was lost to a torn write. A regression that drops the lock around
// append both trips -race AND can make fired < N.
func TestRegisterConcurrentAppends(t *testing.T) {
	t.Parallel()

	const goroutines = 200

	for iter := 0; iter < 50; iter++ {
		srv := &Server{}

		var fired int32
		var wg sync.WaitGroup
		start := make(chan struct{})

		wg.Add(goroutines)
		for i := 0; i < goroutines; i++ {
			go func() {
				defer wg.Done()
				<-start
				srv.RegisterOnShutdown(func() { atomic.AddInt32(&fired, 1) })
			}()
		}
		close(start)
		wg.Wait() // all registrations complete BEFORE Shutdown: deterministic count

		assert.NoError(t, srv.Shutdown())
		waitForInt32(t, &fired, goroutines, time.Second,
			"every concurrently-registered callback must be retained and fire")
	}
}

// TestRegisterAfterShutdownRunsImmediately is the logical proof of the
// production bug and its fix, independent of the race detector.
//
// Scenario: Shutdown has already begun, THEN a request goroutine registers a
// callback (the healthz/ActiveHealthCheck lazy path landing during SIGTERM).
//
// Why it fails without the fix: the unguarded Shutdown ranges s.onShutdown once
// and returns; a callback appended afterward is never ranged again, so it never
// runs — exactly "healthz never flips to 503, the draining pod stays Ready, the
// LB keeps routing -> 502s on every deploy". The channel receive times out.
//
// With the fix RegisterOnShutdown sees shuttingDown==true and runs f via
// go f() immediately, so the callback fires and the receive succeeds.
func TestRegisterAfterShutdownRunsImmediately(t *testing.T) {
	t.Parallel()

	srv := &Server{}
	assert.NoError(t, srv.Shutdown())

	ran := make(chan struct{})
	srv.RegisterOnShutdown(func() { close(ran) })

	select {
	case <-ran:
		// callback ran via the run-now path; correct.
	case <-time.After(time.Second):
		t.Fatal("callback registered after Shutdown began was never run " +
			"(lost registration => healthz never flips to 503)")
	}
}

// TestRegisterDuringShutdownNoDrop closes the exact interleave the lock must
// linearize: a registration that races Shutdown's flag flip is EITHER in the
// snapshot OR run immediately, never neither.
//
// We cannot force the kernel scheduler to land the register inside the window,
// so we hammer: for each trial a registrant and Shutdown start together; the
// registrant's callback must fire regardless of which side won the lock. There
// is no third interleave (proven in the design): RegisterOnShutdown and
// Shutdown contend on the same mutex, so one of the two strictly precedes the
// other under the lock.
//
// Why it fails without the fix: when the append lands after the range, the
// callback is dropped and never fires -> timeout. With the fix it is always
// either snapshotted-and-fired or run-now-fired.
func TestRegisterDuringShutdownNoDrop(t *testing.T) {
	t.Parallel()

	for iter := 0; iter < 500; iter++ {
		srv := &Server{}

		ran := make(chan struct{}, 1)
		start := make(chan struct{})

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			srv.RegisterOnShutdown(func() { ran <- struct{}{} })
		}()
		go func() {
			defer wg.Done()
			<-start
			assert.NoError(t, srv.Shutdown())
		}()
		close(start)
		wg.Wait()

		select {
		case <-ran:
			// fired via snapshot or run-now; correct for every interleave.
		case <-time.After(2 * time.Second):
			t.Fatalf("iter %d: callback registered during Shutdown was neither "+
				"snapshotted nor run immediately (dropped)", iter)
		}
	}
}

// TestDoubleShutdownNoDoubleFire proves double-Shutdown is idempotent for the
// fire+wait phase and does not deadlock.
//
// Real trigger: ListenAndServe's SIGTERM handler calls Shutdown while a test or
// caller also calls it directly.
//
// Why it fails without the fix: the unguarded Shutdown ranges and fires every
// callback on EVERY call, so two Shutdowns fire each callback twice (counter ==
// 2) and sleep WaitBeforeShutdown twice. With the fix `first := !s.shuttingDown`
// + niling onShutdown under the lock means only the first call fires the
// callbacks and waits; the second skips straight to s.s.Shutdown. counter == 1.
//
// Also runs the two Shutdowns concurrently to assert no race / no deadlock:
// neither call holds muShutdown across the sleep or the go f() launch.
func TestDoubleShutdownNoDoubleFire(t *testing.T) {
	t.Parallel()

	// Sequential: exact-once fire.
	t.Run("sequential", func(t *testing.T) {
		srv := &Server{}
		var fired int32
		done := make(chan struct{})
		srv.RegisterOnShutdown(func() {
			atomic.AddInt32(&fired, 1)
			done <- struct{}{}
		})

		assert.NoError(t, srv.Shutdown())
		<-done // first call launched the callback exactly once

		assert.NoError(t, srv.Shutdown()) // second call must NOT re-fire

		// Give any erroneous second launch room to land, then assert exactly one.
		waitForInt32(t, &fired, 1, time.Second, "callback must fire exactly once across two Shutdowns")
		// And no further fire is queued on the channel.
		select {
		case <-done:
			t.Fatal("callback fired a second time on the second Shutdown")
		case <-time.After(50 * time.Millisecond):
		}
	})

	// Concurrent: no race, no deadlock, still exactly-once.
	t.Run("concurrent", func(t *testing.T) {
		srv := &Server{}
		var fired int32
		srv.RegisterOnShutdown(func() { atomic.AddInt32(&fired, 1) })

		var wg sync.WaitGroup
		wg.Add(2)
		for i := 0; i < 2; i++ {
			go func() {
				defer wg.Done()
				assert.NoError(t, srv.Shutdown())
			}()
		}
		wg.Wait() // would hang if Shutdown deadlocked

		waitForInt32(t, &fired, 1, time.Second, "concurrent double-Shutdown must fire callback exactly once")
	})
}

// TestShutdownDrainOrdering locks in the observable drain contract so the race
// fix does not silently change behavior: every pre-registered callback fires,
// AND callbacks are launched BEFORE the WaitBeforeShutdown sleep (so the LB has
// time to observe the resulting 503 before the server stops accepting).
//
// Why it fails on a bad fix: if a "fix" drops pre-registered callbacks, not all
// of them run (wg.Wait times out). If a fix reorders the sleep before the
// launch, the callbacks would be observed only after >= WaitBeforeShutdown;
// here we assert they are all observed strictly before Shutdown returns and
// well within the sleep window. (Pre-fix this passes for the fully-before path —
// its job is to pin the contract.)
//
// Re-entrancy sub-case: one callback calls RegisterOnShutdown again. With the
// fix it goes through the run-now path (Shutdown already unlocked muShutdown),
// so it cannot deadlock; the test would hang if a fix held the lock across
// go f().
func TestShutdownDrainOrdering(t *testing.T) {
	t.Parallel()

	const callbacks = 4
	const waitBefore = 200 * time.Millisecond
	srv := &Server{WaitBeforeShutdown: waitBefore}

	var wg sync.WaitGroup
	wg.Add(callbacks)

	var reentrantRan int32
	for i := 0; i < callbacks-1; i++ {
		srv.RegisterOnShutdown(func() { wg.Done() })
	}
	// Last callback re-enters RegisterOnShutdown to prove the run-now path does
	// not self-deadlock: Shutdown has already released muShutdown by the time
	// callbacks run, so this registration takes the go f() branch.
	srv.RegisterOnShutdown(func() {
		srv.RegisterOnShutdown(func() { atomic.StoreInt32(&reentrantRan, 1) })
		wg.Done()
	})

	startT := time.Now()
	done := make(chan time.Duration, 1)
	go func() {
		assert.NoError(t, srv.Shutdown())
		done <- time.Since(startT)
	}()

	// Record when all callbacks have fired, relative to Shutdown's start. A
	// genuinely dropped registration never fires, so the generous timeout (far
	// above the sleep) distinguishes a drop from a merely-slow schedule.
	fired := make(chan time.Duration, 1)
	go func() { wg.Wait(); fired <- time.Since(startT) }()

	var fireElapsed time.Duration
	select {
	case fireElapsed = <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("not all pre-registered callbacks fired (dropped registration)")
	}

	// Ordering, one-sided with a large margin: callbacks are launched BEFORE the
	// WaitBeforeShutdown sleep, so they fire near t=0 — far under the waitBefore
	// bar. A regression that sleeps before firing pushes fireElapsed to
	// ~waitBefore and trips this. The margin (fire near 0 vs a 150ms bar) dwarfs
	// any realistic scheduler stall for four trivial wg.Done callbacks, so the
	// bar itself is not a flake source.
	assert.Less(t, fireElapsed, waitBefore-50*time.Millisecond,
		"callbacks must launch before the WaitBeforeShutdown sleep, not after")

	total := <-done
	assert.GreaterOrEqual(t, total, waitBefore,
		"Shutdown must still wait WaitBeforeShutdown after launching callbacks")

	// The re-entrant run-now callback fires asynchronously (go f()); wait on its
	// observable state rather than assuming it ran by now.
	waitForInt32(t, &reentrantRan, 1, time.Second,
		"re-entrant RegisterOnShutdown (run-now path) must fire without deadlock")
}

// waitForInt32 spins on observable atomic state until it reaches want or the
// deadline passes — deterministic synchronization, never a bare sleep-and-hope.
func waitForInt32(t *testing.T, p *int32, want int32, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(p) == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	assert.Equal(t, want, atomic.LoadInt32(p), msg)
}
