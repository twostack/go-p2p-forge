package middleware

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	forge "github.com/twostack/go-p2p-forge"
)

// The whole point of reporting a wait is that it is long enough to work. A
// client that retries exactly when told and is rejected again learns to stop
// believing the number and starts guessing, which is the behaviour this
// replaces.
func TestRetryAfterIsLongEnoughToSucceed(t *testing.T) {
	id := generateTestPeerID(t)
	// 10 tokens per second, burst 1: one request, then a ~100ms wait.
	b := NewTokenBucket(time.Second, 10, 1)
	defer b.Close()

	if ok, _ := b.AllowNWithRetry(id, 1); !ok {
		t.Fatal("first request rejected against a full bucket")
	}

	ok, wait := b.AllowNWithRetry(id, 1)
	if ok {
		t.Fatal("second request admitted against an empty bucket")
	}
	if wait <= 0 {
		t.Fatal("rejected with no wait reported")
	}
	if wait > 500*time.Millisecond {
		t.Errorf("wait = %v, want roughly 100ms", wait)
	}

	time.Sleep(wait)
	if ok, again := b.AllowNWithRetry(id, 1); !ok {
		t.Errorf("still rejected after waiting the reported %v (now says %v)", wait, again)
	}
}

// The wait has to scale with the shortfall, or it is a constant wearing a
// duration's clothes.
func TestRetryAfterScalesWithTheShortfall(t *testing.T) {
	id := generateTestPeerID(t)
	b := NewTokenBucket(time.Second, 10, 10)
	defer b.Close()

	if ok, _ := b.AllowNWithRetry(id, 10); !ok {
		t.Fatal("draining the bucket was rejected")
	}

	_, small := b.AllowNWithRetry(id, 1)
	_, large := b.AllowNWithRetry(id, 8)
	if small <= 0 || large <= 0 {
		t.Fatalf("waits = %v and %v, want both positive", small, large)
	}
	if large <= small {
		t.Errorf("waiting for 8 tokens (%v) is not longer than for 1 (%v)", large, small)
	}
}

// A request costing more than the bucket can ever hold is not a timing
// problem. Naming a wait would send the caller back to fail again.
func TestNoRetryOfferedForAnImpossibleRequest(t *testing.T) {
	id := generateTestPeerID(t)
	b := NewTokenBucket(time.Second, 10, 5)
	defer b.Close()

	ok, wait := b.AllowNWithRetry(id, 6)
	if ok {
		t.Fatal("a request costing more than the burst was admitted")
	}
	if wait != 0 {
		t.Errorf("wait = %v for a request no wait can admit, want 0", wait)
	}
}

func TestUnlimitedBucketReportsNoWait(t *testing.T) {
	id := generateTestPeerID(t)
	b := NewTokenBucket(time.Second, 0, 0)
	defer b.Close()

	for i := 0; i < 100; i++ {
		ok, wait := b.AllowNWithRetry(id, 5)
		if !ok || wait != 0 {
			t.Fatalf("unlimited bucket returned ok=%v wait=%v", ok, wait)
		}
	}
}

func TestDualBucketReportsTheWaitOfTheChargedSide(t *testing.T) {
	id := generateTestPeerID(t)
	// Writes are scarce, reads are plentiful.
	b := NewDualTokenBucket(time.Second, 1000, 1000, 1, 1)
	defer b.Close()

	if ok, _ := b.AllowNWithRetry(id, 1, true); !ok {
		t.Fatal("first write rejected")
	}

	ok, wait := b.AllowNWithRetry(id, 1, true)
	if ok || wait <= 0 {
		t.Fatalf("second write: ok=%v wait=%v, want rejected with a wait", ok, wait)
	}

	// The read bucket is untouched and must not inherit the write bucket's
	// exhaustion.
	if ok, wait := b.AllowNWithRetry(id, 1, false); !ok || wait != 0 {
		t.Errorf("read: ok=%v wait=%v, want admitted", ok, wait)
	}
}

// The middleware must pass the wait on, and the error must still satisfy every
// errors.Is check written against the sentinel.
func TestMiddlewareCarriesTheWaitAndKeepsTheSentinel(t *testing.T) {
	id := generateTestPeerID(t)
	b := NewTokenBucket(time.Second, 10, 1)
	defer b.Close()

	mw := RateLimitMiddleware(b)

	first := &forge.StreamContext{PeerID: id, Logger: discardLogger()}
	mw(first, func() {})
	if first.Err != nil {
		t.Fatalf("first request errored: %v", first.Err)
	}

	second := &forge.StreamContext{PeerID: id, Logger: discardLogger()}
	mw(second, func() { t.Error("a rate-limited request reached the handler") })

	if !errors.Is(second.Err, forge.ErrRateLimited) {
		t.Fatalf("err = %v, want it to satisfy errors.Is(ErrRateLimited)", second.Err)
	}

	var rl *forge.RateLimitedError
	if !errors.As(second.Err, &rl) {
		t.Fatalf("err = %v, want a *RateLimitedError carrying the wait", second.Err)
	}
	if rl.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %v, want a positive wait", rl.RetryAfter)
	}
	if !strings.Contains(rl.Error(), "retry after") {
		t.Errorf("message %q does not mention the wait", rl.Error())
	}
}

// A limiter with no retry-reporting form must still work, and must still
// produce the bare sentinel rather than an error promising a wait of zero.
func TestMiddlewareFallsBackForAPlainLimiter(t *testing.T) {
	id := generateTestPeerID(t)
	mw := RateLimitMiddleware(alwaysDeny{})

	sc := &forge.StreamContext{PeerID: id, Logger: discardLogger()}
	mw(sc, func() { t.Error("a rejected request reached the handler") })

	if !errors.Is(sc.Err, forge.ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", sc.Err)
	}
	var rl *forge.RateLimitedError
	if errors.As(sc.Err, &rl) {
		t.Errorf("a limiter that cannot report a wait produced %v", rl)
	}
}

type alwaysDeny struct{}

func (alwaysDeny) Allow(peer.ID) bool       { return false }
func (alwaysDeny) AllowN(peer.ID, int) bool { return false }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
