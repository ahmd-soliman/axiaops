package sso_test

// sweep_test.go pins the §5.5 acceptance "ssoSweep ticker runs and logs
// sweep count > 0 after seeded expired rows." Production sweeper is in
// services/api/internal/sso/sweep.go.
//
// Two properties this file enforces:
//   1. Lifecycle: Run() fires a kick-off tick immediately, then ticks on
//      the configured interval, continues past store errors, and exits
//      cleanly when ctx is cancelled.
//   2. Observability: when SweepStaleSSODomains returns N>0, the sweeper
//      emits an `sso: sweep stale domains` slog.Info entry with
//      `marked_stale=N`. When N=0 (no expired rows), the entry is
//      suppressed — otherwise the production log would carry a noisy
//      "marked_stale=0" line every 24h forever.
//
// Tests do NOT run in parallel: a few of them swap slog.Default() for a
// capturing handler and the swap is process-global. They're fast (sub-
// second each), so serial execution is fine.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"axiaops.io/api/internal/sso"
	"axiaops.io/shared/storage"
)

// ─── fakes ──────────────────────────────────────────────────────────────────

// fakeSweepStore satisfies storage.Store via the embedded-nil-interface
// trick — only SweepStaleSSODomains is implemented. Any other method
// panics, which is the right posture: Sweeper.tick MUST only call this
// one method, and a future change that adds another store call should
// surface here loudly rather than passing tests by accident.
type fakeSweepStore struct {
	storage.Store

	sweepCh chan time.Time // every call sends `now`; capacity-buffered so the test can drain at its own pace
	mu      sync.Mutex     // guards err, count
	err     error
	count   int64
}

func (f *fakeSweepStore) SweepStaleSSODomains(_ context.Context, now time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Non-blocking send: if the buffer is full the test isn't draining
	// fast enough — drop the signal rather than deadlocking the sweeper
	// goroutine. The interval-tick test is the only one that depends on
	// observing > 1 tick, and it sizes the buffer accordingly.
	select {
	case f.sweepCh <- now:
	default:
	}
	return f.count, f.err
}

// syncBuffer is a thread-safe bytes.Buffer suitable for slog handlers.
// slog can write from any goroutine; bytes.Buffer alone races.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureSlog redirects slog.Default() to a fresh text handler writing to
// a thread-safe buffer for the duration of the test. Cleanup restores the
// original Default. Process-global — tests that use this MUST NOT run
// with t.Parallel().
func captureSlog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// runSweeperUntilFirstTick spins up Sweeper.Run on a goroutine, waits for
// the first tick to register on store.sweepCh, then cancels the context
// and waits for the goroutine to exit. Encapsulates the lifecycle dance
// so individual tests don't repeat it.
func runSweeperUntilFirstTick(t *testing.T, sw *sso.Sweeper, store *fakeSweepStore) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sw.Run(ctx)
		close(done)
	}()
	select {
	case <-store.sweepCh:
		// got the tick
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("kick-off tick did not fire within 2s")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit within 2s of context cancel")
	}
}

// ─── lifecycle ──────────────────────────────────────────────────────────────

// TestSweeper_KickOffTickFiresOnStart pins that Run() does not wait the
// full interval before the first tick — important because the default
// interval is 24h, and a binary that just started after a crash should
// catch up on stale domains immediately rather than 24h later.
func TestSweeper_KickOffTickFiresOnStart(t *testing.T) {
	store := &fakeSweepStore{sweepCh: make(chan time.Time, 1), count: 3}
	sw := sso.NewSweeper(store, 24*time.Hour) // long interval so ONLY kick-off fires within the window
	runSweeperUntilFirstTick(t, sw, store)
}

// TestSweeper_TicksOnInterval pins that subsequent ticks fire on the
// configured cadence, not just the initial kick-off.
func TestSweeper_TicksOnInterval(t *testing.T) {
	store := &fakeSweepStore{sweepCh: make(chan time.Time, 5), count: 1}
	sw := sso.NewSweeper(store, 30*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sw.Run(ctx); close(done) }()
	defer func() {
		cancel()
		<-done
	}()

	// Kick-off + 2 interval ticks expected within ~1s on any sane CI.
	for i := 0; i < 3; i++ {
		select {
		case <-store.sweepCh:
		case <-time.After(time.Second):
			t.Fatalf("tick %d did not fire within 1s", i+1)
		}
	}
}

// TestSweeper_ContinuesAfterStoreError pins the production posture from
// sweep.go: a transient DB issue logs and continues, never takes the
// ticker out. Without this, a single failing sweep would leave stale
// domains routing logins forever.
func TestSweeper_ContinuesAfterStoreError(t *testing.T) {
	store := &fakeSweepStore{
		sweepCh: make(chan time.Time, 5),
		err:     errors.New("transient db hiccup"),
	}
	sw := sso.NewSweeper(store, 30*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sw.Run(ctx); close(done) }()
	defer func() {
		cancel()
		<-done
	}()

	// Even though every tick errors, ticker MUST continue past the error
	// and fire at least 3 times (kick-off + 2 interval ticks).
	for i := 0; i < 3; i++ {
		select {
		case <-store.sweepCh:
		case <-time.After(time.Second):
			t.Fatalf("tick %d did not fire despite store errors (ticker died on error?)", i+1)
		}
	}
}

// TestSweeper_StopsOnContextCancel pins that Run() returns promptly when
// the parent context is cancelled. cmd/main.go cancels on shutdown — a
// blocking Run would deadlock the orderly-shutdown path.
func TestSweeper_StopsOnContextCancel(t *testing.T) {
	store := &fakeSweepStore{sweepCh: make(chan time.Time, 1)}
	sw := sso.NewSweeper(store, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sw.Run(ctx); close(done) }()

	// Wait for kick-off tick to prove the goroutine started before
	// asking it to stop.
	select {
	case <-store.sweepCh:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("kick-off tick did not fire")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return within 1s of context cancel")
	}
}

// ─── observability ──────────────────────────────────────────────────────────

// TestSweeper_LogsMarkedStaleCountWhenNonZero pins the §5.5 acceptance
// directly: when the sweep marks N>0 expired rows as stale, slog emits
// `sso: sweep stale domains` with `marked_stale=N`. This is the line ops
// will grep for to confirm the ticker is alive in production logs.
func TestSweeper_LogsMarkedStaleCountWhenNonZero(t *testing.T) {
	logs := captureSlog(t)
	store := &fakeSweepStore{sweepCh: make(chan time.Time, 1), count: 7}
	sw := sso.NewSweeper(store, 24*time.Hour)
	runSweeperUntilFirstTick(t, sw, store)

	out := logs.String()
	if !strings.Contains(out, "sso: sweep stale domains") {
		t.Errorf("expected log line %q; got:\n%s", "sso: sweep stale domains", out)
	}
	if !strings.Contains(out, "marked_stale=7") {
		t.Errorf("expected log line to carry marked_stale=7; got:\n%s", out)
	}
}

// TestSweeper_NoLogWhenMarkedStaleZero pins the inverse: when the sweep
// finds zero expired rows, the info-level log is suppressed. Without
// this branch (sweep.go's `else if n > 0`), every 24h tick would emit a
// `marked_stale=0` line, which becomes log noise that drowns out the
// signal we actually care about.
func TestSweeper_NoLogWhenMarkedStaleZero(t *testing.T) {
	logs := captureSlog(t)
	store := &fakeSweepStore{sweepCh: make(chan time.Time, 1), count: 0}
	sw := sso.NewSweeper(store, 24*time.Hour)
	runSweeperUntilFirstTick(t, sw, store)

	out := logs.String()
	if strings.Contains(out, "marked_stale") {
		t.Errorf("zero-count tick should NOT log marked_stale; got:\n%s", out)
	}
}

// TestSweeper_LogsErrorWhenStoreFails pins the production error-logging
// branch (slog.Error) so a transient DB issue is observable in logs.
// Pairs with TestSweeper_ContinuesAfterStoreError, which proves the
// ticker keeps running; this proves operators will know about it.
func TestSweeper_LogsErrorWhenStoreFails(t *testing.T) {
	logs := captureSlog(t)
	store := &fakeSweepStore{
		sweepCh: make(chan time.Time, 1),
		err:     errors.New("transient db hiccup"),
	}
	sw := sso.NewSweeper(store, 24*time.Hour)
	runSweeperUntilFirstTick(t, sw, store)

	out := logs.String()
	if !strings.Contains(out, "sso: sweep stale domains failed") {
		t.Errorf("expected error log; got:\n%s", out)
	}
	// The underlying error should be attached so on-call can pivot
	// directly to the DB rather than from a generic "failed" line.
	if !strings.Contains(out, "transient db hiccup") {
		t.Errorf("expected error log to carry the wrapped error message; got:\n%s", out)
	}
}
