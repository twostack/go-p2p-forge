package middleware_test

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	forge "github.com/twostack/go-p2p-forge"
	"github.com/twostack/go-p2p-forge/codec"
	"github.com/twostack/go-p2p-forge/forgetest"
	"github.com/twostack/go-p2p-forge/middleware"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestOperationRouter_Dispatch(t *testing.T) {
	pool := codec.NewBufferPool()
	logger := testLogger()

	var dispatched string
	routes := map[string]forge.Middleware{
		"echo": func(sc *forge.StreamContext, next func()) {
			dispatched = "echo"
			sc.Response = map[string]string{"routed": "echo"}
		},
		"store": func(sc *forge.StreamContext, next func()) {
			dispatched = "store"
			sc.Response = map[string]string{"routed": "store"}
		},
	}

	pipeline := forge.NewPipeline(logger,
		forge.JSONResponseWriter(),
		forge.FrameDecodeMiddleware(pool),
		middleware.OperationRouter("op", routes),
	)

	reqData, _ := json.Marshal(map[string]string{"op": "echo", "message": "hi"})
	pid := forgetest.GenerateTestPeerID()
	stream := forgetest.NewMockStreamWithFrame(pid, reqData)
	pipeline.HandleStream(stream)

	if dispatched != "echo" {
		t.Errorf("expected 'echo' route, got %q", dispatched)
	}

	resp, err := stream.ReadResponseFrame()
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]string
	json.Unmarshal(resp, &result)
	if result["routed"] != "echo" {
		t.Errorf("expected routed=echo, got %v", result)
	}
}

func TestOperationRouter_MissingField(t *testing.T) {
	pool := codec.NewBufferPool()
	logger := testLogger()

	routes := map[string]forge.Middleware{
		"echo": func(sc *forge.StreamContext, next func()) {},
	}

	var gotErr error
	pipeline := forge.NewPipeline(logger,
		func(sc *forge.StreamContext, next func()) {
			next()
			gotErr = sc.Err
		},
		forge.FrameDecodeMiddleware(pool),
		middleware.OperationRouter("op", routes),
	)

	reqData, _ := json.Marshal(map[string]string{"message": "hi"})
	pid := forgetest.GenerateTestPeerID()
	stream := forgetest.NewMockStreamWithFrame(pid, reqData)
	pipeline.HandleStream(stream)

	if !errors.Is(gotErr, middleware.ErrMissingOperation) {
		t.Errorf("expected ErrMissingOperation, got: %v", gotErr)
	}
}

func TestOperationRouter_UnknownOp(t *testing.T) {
	pool := codec.NewBufferPool()
	logger := testLogger()

	routes := map[string]forge.Middleware{
		"echo": func(sc *forge.StreamContext, next func()) {},
	}

	var gotErr error
	pipeline := forge.NewPipeline(logger,
		func(sc *forge.StreamContext, next func()) {
			next()
			gotErr = sc.Err
		},
		forge.FrameDecodeMiddleware(pool),
		middleware.OperationRouter("op", routes),
	)

	reqData, _ := json.Marshal(map[string]string{"op": "unknown"})
	pid := forgetest.GenerateTestPeerID()
	stream := forgetest.NewMockStreamWithFrame(pid, reqData)
	pipeline.HandleStream(stream)

	if !errors.Is(gotErr, middleware.ErrUnknownOperation) {
		t.Errorf("expected ErrUnknownOperation, got: %v", gotErr)
	}
}

func TestOperationRouter_NonStringField(t *testing.T) {
	pool := codec.NewBufferPool()
	logger := testLogger()

	routes := map[string]forge.Middleware{
		"echo": func(sc *forge.StreamContext, next func()) {},
	}

	var gotErr error
	pipeline := forge.NewPipeline(logger,
		func(sc *forge.StreamContext, next func()) {
			next()
			gotErr = sc.Err
		},
		forge.FrameDecodeMiddleware(pool),
		middleware.OperationRouter("op", routes),
	)

	// op is an integer, not a string
	reqData, _ := json.Marshal(map[string]any{"op": 42})
	pid := forgetest.GenerateTestPeerID()
	stream := forgetest.NewMockStreamWithFrame(pid, reqData)
	pipeline.HandleStream(stream)

	if gotErr == nil {
		t.Fatal("expected error for non-string operation field")
	}
}

func TestChain_ComposesMiddleware(t *testing.T) {
	var order []string

	chained := middleware.Chain(
		func(sc *forge.StreamContext, next func()) {
			order = append(order, "a")
			next()
		},
		func(sc *forge.StreamContext, next func()) {
			order = append(order, "b")
			next()
		},
	)

	logger := testLogger()
	pipeline := forge.NewPipeline(logger,
		chained,
		func(sc *forge.StreamContext, next func()) {
			order = append(order, "terminal")
		},
	)

	pid := forgetest.GenerateTestPeerID()
	stream := forgetest.NewMockStream(pid, nil)
	pipeline.HandleStream(stream)

	expected := []string{"a", "b", "terminal"}
	if len(order) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, order)
	}
	for i, e := range expected {
		if order[i] != e {
			t.Errorf("position %d: expected %q, got %q", i, e, order[i])
		}
	}
}

func TestOperationRouter_EndToEnd(t *testing.T) {
	pool := codec.NewBufferPool()
	logger := testLogger()

	type echoReq struct {
		Op      string `json:"op"`
		Message string `json:"message"`
	}
	type echoResp struct {
		Echo string `json:"echo"`
	}

	routes := map[string]forge.Middleware{
		"echo": middleware.Chain(
			forge.JSONDeserialize[echoReq](),
			func(sc *forge.StreamContext, next func()) {
				req := sc.Request.(*echoReq)
				sc.Response = &echoResp{Echo: req.Message}
			},
		),
	}

	pipeline := forge.NewPipeline(logger,
		forge.JSONResponseWriter(),
		forge.FrameDecodeMiddleware(pool),
		middleware.OperationRouter("op", routes),
	)

	reqData, _ := json.Marshal(echoReq{Op: "echo", Message: "routed-hello"})
	pid := forgetest.GenerateTestPeerID()
	stream := forgetest.NewMockStreamWithFrame(pid, reqData)
	pipeline.HandleStream(stream)

	resp, err := stream.ReadResponseFrame()
	if err != nil {
		t.Fatal(err)
	}

	var result echoResp
	json.Unmarshal(resp, &result)
	if result.Echo != "routed-hello" {
		t.Errorf("expected 'routed-hello', got %q", result.Echo)
	}
}
