package node

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/libp2p/go-libp2p"
)

func newTestNode(t *testing.T) *Node {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	n, err := New(context.Background(), &Config{DHTMode: DHTModeClient, EnablePubSub: true},
		h, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	t.Cleanup(func() { _ = n.Close() })
	return n
}

// A left topic is gone from the node: publishing to it fails until it is
// joined again, and leaving it twice is harmless.
func TestLeaveTopicUndoesJoin(t *testing.T) {
	n := newTestNode(t)
	const topic = "/test/leave"

	if err := n.JoinTopic(topic); err != nil {
		t.Fatalf("join: %v", err)
	}
	if n.Subscribe(topic) == nil {
		t.Fatal("joined topic has no subscription")
	}

	if err := n.LeaveTopic(topic); err != nil {
		t.Fatalf("leave: %v", err)
	}
	if n.Subscribe(topic) != nil {
		t.Fatal("left topic still has a subscription")
	}
	if err := n.Publish(context.Background(), topic, []byte("x")); err == nil {
		t.Fatal("publish to a left topic succeeded")
	}
	if err := n.LeaveTopic(topic); err != nil {
		t.Fatalf("second leave: %v", err)
	}

	if err := n.JoinTopic(topic); err != nil {
		t.Fatalf("rejoin: %v", err)
	}
	if err := n.Publish(context.Background(), topic, []byte("x")); err != nil {
		t.Fatalf("publish after rejoin: %v", err)
	}
}
