package middleware_test

import (
	"testing"

	forge "github.com/twostack/go-p2p-forge"
	"github.com/twostack/go-p2p-forge/middleware"
)

// newOpContext builds a StreamContext carrying a JSON envelope, with the
// logger a router needs.
func newOpContext(t *testing.T, payload string) *forge.StreamContext {
	t.Helper()
	return &forge.StreamContext{
		RawBytes: []byte(payload),
		Logger:   testLogger(),
	}
}

func TestOperationRouterRecordsRoutedOperation(t *testing.T) {
	var seen string
	routes := map[string]forge.Middleware{
		"GET": func(sc *forge.StreamContext, next func()) { seen = sc.Operation() },
	}

	sc := newOpContext(t, `{"operation":"GET"}`)
	middleware.OperationRouter("operation", routes)(sc, func() {})

	if sc.Err != nil {
		t.Fatalf("routing failed: %v", sc.Err)
	}
	if seen != "GET" {
		t.Errorf("handler saw operation %q, want GET", seen)
	}
	if got := sc.Operation(); got != "GET" {
		t.Errorf("after routing, Operation() = %q, want GET", got)
	}
}

// The operation is recorded only after a route matches. That is the property
// that makes it safe as a metric label: the values are bounded by the routing
// table, not by what a caller decides to send.
func TestOperationRouterRecordsNothingForUnroutableRequests(t *testing.T) {
	routes := map[string]forge.Middleware{
		"GET": func(sc *forge.StreamContext, next func()) {},
	}

	cases := map[string]string{
		"unknown operation": `{"operation":"' OR 1=1; DROP TABLE"}`,
		"missing field":     `{"somethingElse":"GET"}`,
		"not a string":      `{"operation":42}`,
	}

	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			sc := newOpContext(t, payload)
			middleware.OperationRouter("operation", routes)(sc, func() {})

			if sc.Err == nil {
				t.Fatal("expected an error")
			}
			if got := sc.Operation(); got != "" {
				t.Errorf("Operation() = %q, want empty — an unrouted value must never be recorded", got)
			}
		})
	}
}

func TestOperationIsEmptyWithoutARouter(t *testing.T) {
	sc := &forge.StreamContext{}
	if got := sc.Operation(); got != "" {
		t.Errorf("Operation() = %q on a fresh context, want empty", got)
	}
}
