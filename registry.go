package forge

import "fmt"

// Registry is a type-safe container for server-level singletons.
// It is populated during server setup via Server.Provide() and made
// available to handlers through StreamContext.
type Registry struct {
	values map[string]any
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{values: make(map[string]any)}
}

// Provide stores a named value in the registry.
// Panics if the key is already registered (fail-fast on duplicate bindings).
func (r *Registry) Provide(key string, value any) {
	if _, exists := r.values[key]; exists {
		panic(fmt.Sprintf("forge: duplicate registry key %q", key))
	}
	r.values[key] = value
}

// get retrieves a raw value by key.
func (r *Registry) get(key string) (any, bool) {
	v, ok := r.values[key]
	return v, ok
}

// Service retrieves a typed value from the registry.
// Returns the zero value of T and false if the key is missing or the type does not match.
func Service[T any](r *Registry, key string) (T, bool) {
	v, ok := r.get(key)
	if !ok {
		var zero T
		return zero, false
	}
	typed, ok := v.(T)
	if !ok {
		var zero T
		return zero, false
	}
	return typed, true
}

// ServiceFrom retrieves a typed server-level singleton from the StreamContext's registry.
// Returns the zero value and false if the registry is nil, key is missing,
// or the type assertion fails.
func ServiceFrom[T any](sc *StreamContext, key string) (T, bool) {
	if sc.Registry == nil {
		var zero T
		return zero, false
	}
	return Service[T](sc.Registry, key)
}
