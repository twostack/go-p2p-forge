package forge_test

import (
	"testing"

	forge "github.com/twostack/go-p2p-forge"
	"github.com/twostack/go-p2p-forge/forgetest"
)

func TestRegistry_ProvideAndRetrieve(t *testing.T) {
	r := forge.NewRegistry()
	r.Provide("db", "postgres://localhost")

	val, ok := forge.Service[string](r, "db")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if val != "postgres://localhost" {
		t.Errorf("expected 'postgres://localhost', got %q", val)
	}
}

func TestRegistry_MissingKey(t *testing.T) {
	r := forge.NewRegistry()
	_, ok := forge.Service[string](r, "missing")
	if ok {
		t.Error("expected ok=false for missing key")
	}
}

func TestRegistry_TypeMismatch(t *testing.T) {
	r := forge.NewRegistry()
	r.Provide("count", 42)

	_, ok := forge.Service[string](r, "count")
	if ok {
		t.Error("expected ok=false for type mismatch (int vs string)")
	}
}

func TestRegistry_DuplicatePanics(t *testing.T) {
	r := forge.NewRegistry()
	r.Provide("key", "val1")

	defer func() {
		if recover() == nil {
			t.Error("expected panic on duplicate key")
		}
	}()
	r.Provide("key", "val2")
}

func TestServiceFrom_InPipeline(t *testing.T) {
	logger := testLogger()
	reg := forge.NewRegistry()
	reg.Provide("greeting", "hello")

	var result string
	pipeline := forge.NewPipeline(logger,
		func(sc *forge.StreamContext, next func()) {
			v, ok := forge.ServiceFrom[string](sc, "greeting")
			if ok {
				result = v
			}
		},
	).WithRegistry(reg)

	pid := forgetest.GenerateTestPeerID()
	stream := forgetest.NewMockStream(pid, nil)
	pipeline.HandleStream(stream)

	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestServiceFrom_NilRegistry(t *testing.T) {
	logger := testLogger()
	var gotOk bool

	pipeline := forge.NewPipeline(logger,
		func(sc *forge.StreamContext, next func()) {
			_, gotOk = forge.ServiceFrom[string](sc, "anything")
		},
	)

	pid := forgetest.GenerateTestPeerID()
	stream := forgetest.NewMockStream(pid, nil)
	pipeline.HandleStream(stream)

	if gotOk {
		t.Error("expected ok=false when registry is nil")
	}
}
