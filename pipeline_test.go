package forge_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	forge "github.com/twostack/go-p2p-forge"
	"github.com/twostack/go-p2p-forge/codec"
	"github.com/twostack/go-p2p-forge/forgetest"
	"github.com/twostack/go-p2p-forge/middleware"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- Echo handler for end-to-end tests ---

type echoRequest struct {
	Message string `json:"message"`
}

type echoResponse struct {
	Echo string `json:"echo"`
}

func echoHandler(sc *forge.StreamContext, next func()) {
	req := sc.Request.(*echoRequest)
	sc.Response = &echoResponse{Echo: req.Message}
}

// --- Tests ---

func TestPipeline_EndToEnd(t *testing.T) {
	pool := codec.NewBufferPool()
	logger := testLogger()

	pipeline := forge.NewPipeline(logger,
		forge.JSONResponseWriter(),
		forge.FrameDecodeMiddleware(pool),
		forge.JSONDeserialize[echoRequest](),
		forge.Middleware(echoHandler),
	)

	reqData, _ := json.Marshal(echoRequest{Message: "hello"})
	pid := forgetest.GenerateTestPeerID()
	stream := forgetest.NewMockStreamWithFrame(pid, reqData)

	pipeline.HandleStream(stream)

	respData, err := stream.ReadResponseFrame()
	if err != nil {
		t.Fatal(err)
	}

	var resp echoResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		t.Fatal(err)
	}

	if resp.Echo != "hello" {
		t.Errorf("expected echo 'hello', got %q", resp.Echo)
	}
}

func TestPipeline_MiddlewareOrder(t *testing.T) {
	logger := testLogger()
	var order []string

	pipeline := forge.NewPipeline(logger,
		func(sc *forge.StreamContext, next func()) {
			order = append(order, "before:1")
			next()
			order = append(order, "after:1")
		},
		func(sc *forge.StreamContext, next func()) {
			order = append(order, "before:2")
			next()
			order = append(order, "after:2")
		},
		func(sc *forge.StreamContext, next func()) {
			order = append(order, "handler")
		},
	)

	pid := forgetest.GenerateTestPeerID()
	stream := forgetest.NewMockStream(pid, nil)
	pipeline.HandleStream(stream)

	expected := []string{"before:1", "before:2", "handler", "after:2", "after:1"}
	if len(order) != len(expected) {
		t.Fatalf("expected %d events, got %d: %v", len(expected), len(order), order)
	}
	for i, e := range expected {
		if order[i] != e {
			t.Errorf("position %d: expected %q, got %q", i, e, order[i])
		}
	}
}

func TestPipeline_ShortCircuit(t *testing.T) {
	logger := testLogger()
	handlerCalled := false

	pipeline := forge.NewPipeline(logger,
		func(sc *forge.StreamContext, next func()) {
			// Don't call next — short-circuit
			sc.Err = forge.ErrRateLimited
		},
		func(sc *forge.StreamContext, next func()) {
			handlerCalled = true
		},
	)

	pid := forgetest.GenerateTestPeerID()
	stream := forgetest.NewMockStream(pid, nil)
	pipeline.HandleStream(stream)

	if handlerCalled {
		t.Error("handler should not be called after short-circuit")
	}
}

func TestPipeline_Recovery(t *testing.T) {
	logger := testLogger()

	pipeline := forge.NewPipeline(logger,
		middleware.Recovery(),
		func(sc *forge.StreamContext, next func()) {
			panic("test panic")
		},
	)

	pid := forgetest.GenerateTestPeerID()
	stream := forgetest.NewMockStream(pid, nil)

	// Should not panic
	pipeline.HandleStream(stream)
}

func TestPipeline_RateLimitMiddleware(t *testing.T) {
	logger := testLogger()
	pool := codec.NewBufferPool()
	limiter := middleware.NewSingleBucket(time.Minute, 1)
	pid := forgetest.GenerateTestPeerID()

	pipeline := forge.NewPipeline(logger,
		forge.FrameDecodeMiddleware(pool),
		middleware.RateLimitMiddleware(limiter),
		func(sc *forge.StreamContext, next func()) {
			sc.Response = map[string]string{"ok": "true"}
		},
	)

	// First request — allowed
	reqData, _ := json.Marshal(map[string]string{"test": "1"})
	stream1 := forgetest.NewMockStreamWithFrame(pid, reqData)
	pipeline.HandleStream(stream1)

	// Second request — rate limited (no response written since we short-circuit)
	stream2 := forgetest.NewMockStreamWithFrame(pid, reqData)
	pipeline.HandleStream(stream2)

	if stream2.WriteBuf.Len() > 0 {
		t.Error("rate limited request should not produce a response")
	}
}

func TestPipeline_StreamContext_Values(t *testing.T) {
	logger := testLogger()
	var retrievedValue any

	pipeline := forge.NewPipeline(logger,
		func(sc *forge.StreamContext, next func()) {
			sc.Set("key", "value123")
			next()
		},
		func(sc *forge.StreamContext, next func()) {
			v, ok := sc.Get("key")
			if ok {
				retrievedValue = v
			}
		},
	)

	pid := forgetest.GenerateTestPeerID()
	stream := forgetest.NewMockStream(pid, nil)
	pipeline.HandleStream(stream)

	if retrievedValue != "value123" {
		t.Errorf("expected value 'value123', got %v", retrievedValue)
	}
}

func TestPipeline_MultiFrame(t *testing.T) {
	pool := codec.NewBufferPool()
	logger := testLogger()

	type meta struct {
		Count int `json:"count"`
	}
	type msg struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
	}

	pipeline := forge.NewPipeline(logger,
		forge.JSONResponseWriter(),
		forge.FrameDecodeMiddleware(pool),
		func(sc *forge.StreamContext, next func()) {
			sc.Response = forge.NewSliceIterator(forge.JSONCodec{},
				meta{Count: 2},
				msg{ID: 1, Body: "first"},
				msg{ID: 2, Body: "second"},
			)
		},
	)

	reqData, _ := json.Marshal(map[string]string{"action": "retrieve"})
	pid := forgetest.GenerateTestPeerID()
	stream := forgetest.NewMockStreamWithFrame(pid, reqData)

	pipeline.HandleStream(stream)

	// Read 3 frames
	frame1, err := stream.ReadResponseFrame()
	if err != nil {
		t.Fatal("frame 1:", err)
	}
	var m meta
	if err := json.Unmarshal(frame1, &m); err != nil {
		t.Fatal(err)
	}
	if m.Count != 2 {
		t.Errorf("expected count=2, got %d", m.Count)
	}

	frame2, err := stream.ReadResponseFrame()
	if err != nil {
		t.Fatal("frame 2:", err)
	}
	var msg1 msg
	json.Unmarshal(frame2, &msg1)
	if msg1.ID != 1 || msg1.Body != "first" {
		t.Errorf("unexpected msg1: %+v", msg1)
	}

	frame3, err := stream.ReadResponseFrame()
	if err != nil {
		t.Fatal("frame 3:", err)
	}
	var msg2 msg
	json.Unmarshal(frame3, &msg2)
	if msg2.ID != 2 || msg2.Body != "second" {
		t.Errorf("unexpected msg2: %+v", msg2)
	}
}

func TestSliceIterator_Empty(t *testing.T) {
	pool := codec.NewBufferPool()
	logger := testLogger()

	pipeline := forge.NewPipeline(logger,
		forge.JSONResponseWriter(),
		forge.FrameDecodeMiddleware(pool),
		func(sc *forge.StreamContext, next func()) {
			sc.Response = forge.NewSliceIterator(forge.JSONCodec{}) // empty
		},
	)

	reqData, _ := json.Marshal(map[string]string{"action": "empty"})
	pid := forgetest.GenerateTestPeerID()
	stream := forgetest.NewMockStreamWithFrame(pid, reqData)
	pipeline.HandleStream(stream)

	if stream.WriteBuf.Len() > 0 {
		t.Error("expected no frames written for empty iterator")
	}
}

func TestDeserializeMiddleware_GenericJSON(t *testing.T) {
	pool := codec.NewBufferPool()
	logger := testLogger()

	pipeline := forge.NewPipeline(logger,
		forge.ResponseWriterMiddleware(forge.JSONCodec{}),
		forge.FrameDecodeMiddleware(pool),
		forge.DeserializeMiddleware[echoRequest](forge.JSONCodec{}),
		forge.Middleware(echoHandler),
	)

	reqData, _ := json.Marshal(echoRequest{Message: "generic"})
	pid := forgetest.GenerateTestPeerID()
	stream := forgetest.NewMockStreamWithFrame(pid, reqData)

	pipeline.HandleStream(stream)

	respData, err := stream.ReadResponseFrame()
	if err != nil {
		t.Fatal(err)
	}

	var resp echoResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		t.Fatal(err)
	}

	if resp.Echo != "generic" {
		t.Errorf("expected echo 'generic', got %q", resp.Echo)
	}
}

func TestResponseWriterMiddleware_MultiFrame(t *testing.T) {
	pool := codec.NewBufferPool()
	logger := testLogger()

	type item struct {
		N int `json:"n"`
	}

	// Use the generic ResponseWriterMiddleware (not JSONResponseWriter) with multi-frame.
	pipeline := forge.NewPipeline(logger,
		forge.ResponseWriterMiddleware(forge.JSONCodec{}),
		forge.FrameDecodeMiddleware(pool),
		func(sc *forge.StreamContext, next func()) {
			sc.Response = forge.NewSliceIterator(forge.JSONCodec{},
				item{N: 10},
				item{N: 20},
			)
		},
	)

	reqData, _ := json.Marshal(map[string]string{"action": "list"})
	pid := forgetest.GenerateTestPeerID()
	stream := forgetest.NewMockStreamWithFrame(pid, reqData)
	pipeline.HandleStream(stream)

	frame1, err := stream.ReadResponseFrame()
	if err != nil {
		t.Fatal("frame 1:", err)
	}
	var i1 item
	json.Unmarshal(frame1, &i1)
	if i1.N != 10 {
		t.Errorf("expected N=10, got %d", i1.N)
	}

	frame2, err := stream.ReadResponseFrame()
	if err != nil {
		t.Fatal("frame 2:", err)
	}
	var i2 item
	json.Unmarshal(frame2, &i2)
	if i2.N != 20 {
		t.Errorf("expected N=20, got %d", i2.N)
	}
}

func TestPipeline_Use(t *testing.T) {
	logger := testLogger()
	var order []string

	pipeline := forge.NewPipeline(logger)
	pipeline.Use(
		func(sc *forge.StreamContext, next func()) {
			order = append(order, "a")
			next()
		},
		func(sc *forge.StreamContext, next func()) {
			order = append(order, "b")
		},
	)

	pid := forgetest.GenerateTestPeerID()
	stream := forgetest.NewMockStream(pid, nil)
	pipeline.HandleStream(stream)

	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Errorf("expected [a, b], got %v", order)
	}
}
