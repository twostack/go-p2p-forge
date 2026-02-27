package forge

import (
	"context"
	"log/slog"
	"sync"

	"github.com/libp2p/go-libp2p/core/network"
)

// Middleware processes a StreamContext and calls next to continue the chain.
// If next is not called, the pipeline is short-circuited (e.g., rate limit rejection).
// Middleware can perform both pre-processing and post-processing by executing
// code before and after calling next(), similar to Netty's ChannelHandler.
type Middleware func(sc *StreamContext, next func())

// Pipeline is an ordered chain of middleware that processes libp2p streams.
// It implements the Netty ChannelPipeline pattern: middleware executes in order,
// each wrapping the downstream call via next(). This enables bidirectional
// processing (request + response) in a single middleware function.
type Pipeline struct {
	middleware    []Middleware
	logger        *slog.Logger
	registry      *Registry
	activeStreams *sync.WaitGroup
}

// NewPipeline creates a pipeline with the given middleware chain.
// Middleware executes in the order provided.
func NewPipeline(logger *slog.Logger, mw ...Middleware) *Pipeline {
	return &Pipeline{
		middleware: mw,
		logger:     logger,
	}
}

// HandleStream is the entry point that satisfies libp2p's network.StreamHandler.
// It creates a StreamContext, runs the middleware chain, and handles cleanup.
func (p *Pipeline) HandleStream(s network.Stream) {
	if p.activeStreams != nil {
		p.activeStreams.Add(1)
		defer p.activeStreams.Done()
	}

	sc := &StreamContext{
		Ctx:      context.Background(),
		Stream:   s,
		PeerID:   s.Conn().RemotePeer(),
		Logger:   p.logger,
		Registry: p.registry,
	}

	defer func() {
		if sc.PoolBuf != nil {
			sc.PoolBuf.Release()
		}
		s.Close()
	}()

	p.run(sc, 0)
}

// run recursively executes middleware at the given index.
func (p *Pipeline) run(sc *StreamContext, idx int) {
	if sc.Err != nil || idx >= len(p.middleware) {
		return
	}
	p.middleware[idx](sc, func() {
		p.run(sc, idx+1)
	})
}

// WithRegistry sets the service registry for the pipeline.
// The registry is injected into each StreamContext, making server-level
// singletons accessible to handlers via ServiceFrom[T].
func (p *Pipeline) WithRegistry(r *Registry) *Pipeline {
	p.registry = r
	return p
}

// Use appends middleware to the pipeline and returns the pipeline for chaining.
func (p *Pipeline) Use(mw ...Middleware) *Pipeline {
	p.middleware = append(p.middleware, mw...)
	return p
}

// WithActiveStreams sets a WaitGroup that tracks in-flight stream processing.
// The pipeline calls Add(1) on stream entry and Done() on exit, enabling
// graceful shutdown draining in Server.Stop().
func (p *Pipeline) WithActiveStreams(wg *sync.WaitGroup) *Pipeline {
	p.activeStreams = wg
	return p
}

// StreamHandler returns the pipeline's HandleStream method as a network.StreamHandler.
func (p *Pipeline) StreamHandler() network.StreamHandler {
	return p.HandleStream
}
