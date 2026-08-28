package middleware

import (
	"sync"
	"testing"
	"time"
)

func TestTokenBucket_AllowsBurstThenLimits(t *testing.T) {
	// 60 tokens per second, capacity 10: a peer may spend 10 immediately.
	rl := NewTokenBucket(time.Second, 60, 10)
	defer rl.Close()
	pid := generateTestPeerID(t)

	for i := 0; i < 10; i++ {
		if !rl.Allow(pid) {
			t.Fatalf("request %d should be within burst", i)
		}
	}
	if rl.Allow(pid) {
		t.Error("request past the burst capacity should be limited")
	}
}

func TestTokenBucket_BurstExceedsRate(t *testing.T) {
	// The point of a burst allowance: a peer may spend more in one moment
	// than the sustained rate permits per window. A sliding window cannot
	// express this.
	rl := NewTokenBucket(time.Minute, 10, 50)
	defer rl.Close()
	pid := generateTestPeerID(t)

	for i := 0; i < 50; i++ {
		if !rl.Allow(pid) {
			t.Fatalf("request %d should be within the burst of 50", i)
		}
	}
	if rl.Allow(pid) {
		t.Error("request 51 should be limited")
	}
}

func TestTokenBucket_Refills(t *testing.T) {
	// 100 tokens/sec, capacity 2. Draining both then waiting ~50ms should
	// return roughly 5 tokens, so at least one more request must pass.
	rl := NewTokenBucket(time.Second, 100, 2)
	defer rl.Close()
	pid := generateTestPeerID(t)

	if !rl.Allow(pid) || !rl.Allow(pid) {
		t.Fatal("first two requests should be allowed")
	}
	if rl.Allow(pid) {
		t.Fatal("third request should be limited before any refill")
	}

	time.Sleep(50 * time.Millisecond)

	if !rl.Allow(pid) {
		t.Error("a request should be allowed after the bucket refilled")
	}
}

func TestTokenBucket_RefillIsCappedAtBurst(t *testing.T) {
	// An idle peer must not accumulate an unbounded credit.
	rl := NewTokenBucket(100*time.Millisecond, 100, 5)
	defer rl.Close()
	pid := generateTestPeerID(t)

	// Prime the entry, then stay idle for many windows' worth of refill.
	if !rl.Allow(pid) {
		t.Fatal("first request should be allowed")
	}
	time.Sleep(500 * time.Millisecond)

	allowed := 0
	for i := 0; i < 100; i++ {
		if !rl.AllowN(pid, 1) {
			break
		}
		allowed++
	}
	// Capacity is 5; refill during this tight loop is negligible.
	if allowed > 10 {
		t.Errorf("idle peer accumulated %d tokens, capacity is 5", allowed)
	}
}

func TestTokenBucket_AllowNIsAllOrNothing(t *testing.T) {
	// A rejected request must not partially drain the bucket, or a client
	// retrying a too-large batch would starve itself.
	rl := NewTokenBucket(time.Minute, 10, 10)
	defer rl.Close()
	pid := generateTestPeerID(t)

	if !rl.AllowN(pid, 6) {
		t.Fatal("6 of 10 tokens should be available")
	}
	if rl.AllowN(pid, 6) {
		t.Fatal("a second 6 should not fit in the remaining 4")
	}
	// The rejected call must have consumed nothing, leaving all 4.
	if !rl.AllowN(pid, 4) {
		t.Error("the rejected request consumed tokens it should not have")
	}
}

func TestTokenBucket_CostAboveBurstIsRejected(t *testing.T) {
	// No amount of waiting makes this request affordable, so it must fail
	// immediately rather than appear retryable.
	rl := NewTokenBucket(time.Minute, 10, 20)
	defer rl.Close()
	pid := generateTestPeerID(t)

	if rl.AllowN(pid, 21) {
		t.Error("a cost above burst capacity should be rejected")
	}
	if !rl.AllowN(pid, 20) {
		t.Error("a cost equal to burst capacity should be allowed")
	}
}

func TestTokenBucket_ZeroBurstDefaultsToRate(t *testing.T) {
	// An unspecified burst gives a peer one window's worth to spend at once.
	rl := NewTokenBucket(time.Minute, 100, 0)
	defer rl.Close()
	pid := generateTestPeerID(t)

	for i := 0; i < 100; i++ {
		if !rl.Allow(pid) {
			t.Fatalf("request %d should be allowed: burst should default to the rate of 100", i)
		}
	}
	if rl.Allow(pid) {
		t.Error("request 101 should be limited")
	}
}

func TestTokenBucket_BurstBelowRateIsHonoured(t *testing.T) {
	// A capacity under one window's refill paces the peer without lowering
	// its sustained throughput, so it must not be silently corrected upward.
	rl := NewTokenBucket(time.Minute, 100, 10)
	defer rl.Close()
	pid := generateTestPeerID(t)

	for i := 0; i < 10; i++ {
		if !rl.Allow(pid) {
			t.Fatalf("request %d should be within the burst of 10", i)
		}
	}
	if rl.Allow(pid) {
		t.Error("request 11 should be limited: burst of 10 must not be raised to the rate of 100")
	}
}

func TestTokenBucket_ZeroRateIsUnlimited(t *testing.T) {
	rl := NewTokenBucket(time.Minute, 0, 0)
	defer rl.Close()
	pid := generateTestPeerID(t)

	for i := 0; i < 1000; i++ {
		if !rl.Allow(pid) {
			t.Fatalf("request %d should be allowed when limiting is disabled", i)
		}
	}
	if !rl.AllowN(pid, 1_000_000) {
		t.Error("any cost should be allowed when limiting is disabled")
	}
	if got := rl.TrackedPeers(); got != 0 {
		t.Errorf("disabled limiter should keep no state, tracking %d peers", got)
	}
}

func TestTokenBucket_PeersAreIndependent(t *testing.T) {
	rl := NewTokenBucket(time.Minute, 2, 2)
	defer rl.Close()
	a := generateTestPeerID(t)
	b := generateTestPeerID(t)

	if !rl.Allow(a) || !rl.Allow(a) {
		t.Fatal("peer a should get its full allowance")
	}
	if rl.Allow(a) {
		t.Fatal("peer a should now be limited")
	}
	if !rl.Allow(b) || !rl.Allow(b) {
		t.Error("peer b's allowance should be unaffected by peer a")
	}
}

func TestTokenBucket_EvictsIdlePeers(t *testing.T) {
	// State for a peer whose bucket has refilled to capacity is
	// indistinguishable from a peer never seen, so it must not be retained.
	rl := NewTokenBucket(20*time.Millisecond, 100, 100)
	defer rl.Close()

	for i := 0; i < 50; i++ {
		rl.Allow(generateTestPeerID(t))
	}
	if got := rl.TrackedPeers(); got != 50 {
		t.Fatalf("expected 50 tracked peers, got %d", got)
	}

	time.Sleep(60 * time.Millisecond)
	rl.evictIdle()

	if got := rl.TrackedPeers(); got != 0 {
		t.Errorf("expected idle peers to be evicted, %d still tracked", got)
	}
}

func TestTokenBucket_ConcurrentAccessIsSafe(t *testing.T) {
	rl := NewTokenBucket(time.Minute, 500, 500)
	defer rl.Close()
	pid := generateTestPeerID(t)

	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if rl.Allow(pid) {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	// 1000 attempts against a capacity of 500; refill over a few
	// milliseconds is negligible, so the count must not overshoot.
	if allowed > 510 {
		t.Errorf("allowed %d requests, capacity is 500 — tokens leaked under concurrency", allowed)
	}
	if allowed < 500 {
		t.Errorf("allowed only %d requests, capacity is 500 — tokens lost under concurrency", allowed)
	}
}

func TestDualTokenBucket_SeparatesReadAndWrite(t *testing.T) {
	rl := NewDualTokenBucket(time.Minute, 10, 10, 2, 2)
	defer rl.Close()
	pid := generateTestPeerID(t)

	if !rl.Allow(pid, true) || !rl.Allow(pid, true) {
		t.Fatal("both writes should be allowed")
	}
	if rl.Allow(pid, true) {
		t.Fatal("third write should be limited")
	}
	// Exhausting writes must not touch the read allowance.
	for i := 0; i < 10; i++ {
		if !rl.Allow(pid, false) {
			t.Fatalf("read %d should be allowed despite the write bucket being empty", i)
		}
	}
}

func TestDualTokenBucket_AllowNChargesTheRightBucket(t *testing.T) {
	rl := NewDualTokenBucket(time.Minute, 100, 100, 100, 100)
	defer rl.Close()
	pid := generateTestPeerID(t)

	if !rl.AllowN(pid, 100, true) {
		t.Fatal("a 100-unit write should fit the write bucket")
	}
	if rl.AllowN(pid, 1, true) {
		t.Error("the write bucket should now be empty")
	}
	if !rl.AllowN(pid, 100, false) {
		t.Error("the read bucket should be untouched")
	}
}

func TestSingleBucket_AllowNIsAllOrNothing(t *testing.T) {
	rl := NewSingleBucket(time.Minute, 10)
	defer rl.Close()
	pid := generateTestPeerID(t)

	if !rl.AllowN(pid, 6) {
		t.Fatal("6 of 10 should be allowed")
	}
	if rl.AllowN(pid, 6) {
		t.Fatal("a second 6 should not fit in the remaining 4")
	}
	if !rl.AllowN(pid, 4) {
		t.Error("the rejected request consumed budget it should not have")
	}
	if rl.Allow(pid) {
		t.Error("the window should now be full")
	}
}

// The middleware accepts either limiter implementation.
var (
	_ Limiter     = (*SingleBucket)(nil)
	_ Limiter     = (*TokenBucket)(nil)
	_ DualLimiter = (*DualBucket)(nil)
	_ DualLimiter = (*DualTokenBucket)(nil)
)
