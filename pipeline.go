package forge

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
)

const (
	// DefaultIdleTimeout is how long a pipeline waits for a peer to make
	// progress on a frame before abandoning the stream. It is refreshed as
	// bytes arrive, so it bounds stalls, not transfer time.
	DefaultIdleTimeout = 30 * time.Second

	// DefaultRequestTimeout bounds the whole life of an inbound stream: read,
	// handler and response. It is the backstop against a peer that trickles
	// one byte inside every idle window, and it is the deadline handlers see
	// on sc.Ctx.
	DefaultRequestTimeout = 5 * time.Minute
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
	middleware     []Middleware
	logger         *slog.Logger
	registry       *Registry
	activeStreams  *sync.WaitGroup
	idleTimeout    time.Duration
	requestTimeout time.Duration
}

// NewPipeline creates a pipeline with the given middleware chain.
// Middleware executes in the order provided.
//
// Streams are bounded by DefaultIdleTimeout and DefaultRequestTimeout unless
// WithIdleTimeout or WithRequestTimeout say otherwise.
func NewPipeline(logger *slog.Logger, mw ...Middleware) *Pipeline {
	return &Pipeline{
		middleware:     mw,
		logger:         logger,
		idleTimeout:    DefaultIdleTimeout,
		requestTimeout: DefaultRequestTimeout,
	}
}

// HandleStream is the entry point that satisfies libp2p's network.StreamHandler.
// It creates a StreamContext, runs the middleware chain, and handles cleanup.
func (p *Pipeline) HandleStream(s network.Stream) {
	if p.activeStreams != nil {
		p.activeStreams.Add(1)
		defer p.activeStreams.Done()
	}

	ctx, cancel := p.requestContext()
	defer cancel()

	// When the request deadline passes, reset the stream so a read or write
	// blocked on the peer returns instead of waiting for its own idle
	// deadline. Stopping the hook before the normal close below keeps a
	// completed stream from being reset by a late-firing timer.
	stop := context.AfterFunc(ctx, func() { _ = s.Reset() })

	sc := &StreamContext{
		Ctx:         ctx,
		Stream:      s,
		PeerID:      s.Conn().RemotePeer(),
		Logger:      p.logger,
		Registry:    p.registry,
		IdleTimeout: p.idleTimeout,
	}

	defer func() {
		if sc.PoolBuf != nil {
			sc.PoolBuf.Release()
		}
		stop()
		s.Close()
	}()

	p.run(sc, 0)
}

// requestContext derives the per-stream context: cancelled when the stream
// handling ends, and bounded by the request timeout when one is set.
func (p *Pipeline) requestContext() (context.Context, context.CancelFunc) {
	if p.requestTimeout > 0 {
		return context.WithTimeout(context.Background(), p.requestTimeout)
	}
	return context.WithCancel(context.Background())
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

// WithIdleTimeout sets how long a frame read or write may wait for the peer
// to make progress. Zero disables the deadline.
func (p *Pipeline) WithIdleTimeout(d time.Duration) *Pipeline {
	p.idleTimeout = d
	return p
}

// WithRequestTimeout bounds the whole life of a stream and is the deadline
// on sc.Ctx. Zero disables it, leaving only the idle timeout.
func (p *Pipeline) WithRequestTimeout(d time.Duration) *Pipeline {
	p.requestTimeout = d
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
