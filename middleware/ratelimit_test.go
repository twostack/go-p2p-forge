package middleware

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func generateTestPeerID(t *testing.T) peer.ID {
	t.Helper()
	priv, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSingleBucket_AllowsUpToMax(t *testing.T) {
	rl := NewSingleBucket(time.Minute, 3)
	defer rl.Close()
	pid := generateTestPeerID(t)

	for i := 0; i < 3; i++ {
		if !rl.Allow(pid) {
			t.Errorf("request %d should be allowed", i)
		}
	}

	if rl.Allow(pid) {
		t.Error("request 4 should be rate limited")
	}
}

func TestSingleBucket_DifferentPeers(t *testing.T) {
	rl := NewSingleBucket(time.Minute, 1)
	defer rl.Close()
	pid1 := generateTestPeerID(t)
	pid2 := generateTestPeerID(t)

	if !rl.Allow(pid1) {
		t.Error("peer1 first request should be allowed")
	}
	if !rl.Allow(pid2) {
		t.Error("peer2 first request should be allowed (separate bucket)")
	}
	if rl.Allow(pid1) {
		t.Error("peer1 second request should be rate limited")
	}
}

func TestSingleBucket_WindowExpiry(t *testing.T) {
	rl := NewSingleBucket(50*time.Millisecond, 1)
	defer rl.Close()
	pid := generateTestPeerID(t)

	if !rl.Allow(pid) {
		t.Error("first request should be allowed")
	}
	if rl.Allow(pid) {
		t.Error("second request should be rate limited")
	}

	time.Sleep(60 * time.Millisecond)

	if !rl.Allow(pid) {
		t.Error("request after window expiry should be allowed")
	}
}

func TestDualBucket_SeparateLimits(t *testing.T) {
	rl := NewDualBucket(time.Minute, 2, 1)
	defer rl.Close()
	pid := generateTestPeerID(t)

	// Write: limit 1
	if !rl.Allow(pid, true) {
		t.Error("first write should be allowed")
	}
	if rl.Allow(pid, true) {
		t.Error("second write should be rate limited")
	}

	// Read: limit 2 (independent of writes)
	if !rl.Allow(pid, false) {
		t.Error("first read should be allowed")
	}
	if !rl.Allow(pid, false) {
		t.Error("second read should be allowed")
	}
	if rl.Allow(pid, false) {
		t.Error("third read should be rate limited")
	}
}

func TestSingleBucket_ConcurrentAccess(t *testing.T) {
	rl := NewSingleBucket(time.Minute, 100)
	defer rl.Close()

	// Generate 10 distinct peers.
	peers := make([]peer.ID, 10)
	for i := range peers {
		peers[i] = generateTestPeerID(t)
	}

	var wg sync.WaitGroup
	var allowed atomic.Int64

	// 100 goroutines, each making 10 requests with a random peer.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			pid := peers[idx%len(peers)]
			for j := 0; j < 10; j++ {
				if rl.Allow(pid) {
					allowed.Add(1)
				}
			}
		}(i)
	}

	wg.Wait()

	// Each peer has 10 goroutines * 10 requests = 100 requests, limit is 100.
	// All should be allowed (exactly 100 per peer).
	if got := allowed.Load(); got != 1000 {
		t.Errorf("expected 1000 allowed requests, got %d", got)
	}
}

func TestSingleBucket_Cleanup(t *testing.T) {
	rl := NewSingleBucket(50*time.Millisecond, 10)
	defer rl.Close()

	pid := generateTestPeerID(t)
	for i := 0; i < 5; i++ {
		rl.Allow(pid)
	}

	// Wait for entries to expire.
	time.Sleep(60 * time.Millisecond)

	// Trigger cleanup directly.
	rl.cleanup()

	// The peer's entry should have been evicted. Verify by allowing maxRequests again.
	for i := 0; i < 10; i++ {
		if !rl.Allow(pid) {
			t.Errorf("request %d should be allowed after cleanup", i)
		}
	}
	if rl.Allow(pid) {
		t.Error("request 11 should be rate limited")
	}
}

func BenchmarkSingleBucket_Allow_Parallel(b *testing.B) {
	rl := NewSingleBucket(time.Minute, 1000000)
	defer rl.Close()

	// Pre-generate peer IDs.
	peers := make([]peer.ID, 100)
	for i := range peers {
		priv, _, _ := crypto.GenerateEd25519Key(nil)
		id, _ := peer.IDFromPrivateKey(priv)
		peers[i] = id
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			rl.Allow(peers[i%len(peers)])
			i++
		}
	})
}
