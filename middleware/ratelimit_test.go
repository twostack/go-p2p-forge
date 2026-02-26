package middleware

import (
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
