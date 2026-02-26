package codec

import (
	"sync"
	"sync/atomic"
)

// Buffer pool tier sizes.
const (
	tierSmall  = 4 * 1024       // 4 KB — most JSON requests
	tierMedium = 64 * 1024      // 64 KB — larger documents
	tierLarge  = 1 * 1024 * 1024 // 1 MB — images, large payloads
)

// BufferPool manages reusable byte buffers in size-tiered sync.Pools.
// Inspired by Netty's pooled ByteBuf allocator, this reduces GC pressure
// under high throughput by reusing frame buffers across requests.
type BufferPool struct {
	small  sync.Pool
	medium sync.Pool
	large  sync.Pool

	// Metrics (atomic, no lock contention).
	Hits   atomic.Int64
	Misses atomic.Int64
}

// NewBufferPool creates a new tiered buffer pool.
func NewBufferPool() *BufferPool {
	return &BufferPool{
		small: sync.Pool{New: func() any {
			b := make([]byte, tierSmall)
			return &b
		}},
		medium: sync.Pool{New: func() any {
			b := make([]byte, tierMedium)
			return &b
		}},
		large: sync.Pool{New: func() any {
			b := make([]byte, tierLarge)
			return &b
		}},
	}
}

// PoolBuffer wraps a byte slice with its originating pool tier for correct return.
type PoolBuffer struct {
	data     []byte
	pool     *BufferPool
	tier     int // 0=small, 1=medium, 2=large, -1=unmanaged
	released atomic.Bool
}

// Bytes returns the usable portion of the buffer.
func (pb *PoolBuffer) Bytes() []byte { return pb.data }

// Len returns the length of the usable portion.
func (pb *PoolBuffer) Len() int { return len(pb.data) }

// Release returns the buffer to its pool. Safe to call multiple times.
func (pb *PoolBuffer) Release() {
	if !pb.released.CompareAndSwap(false, true) {
		return
	}
	switch pb.tier {
	case 0:
		b := pb.data[:cap(pb.data)]
		pb.pool.small.Put(&b)
	case 1:
		b := pb.data[:cap(pb.data)]
		pb.pool.medium.Put(&b)
	case 2:
		b := pb.data[:cap(pb.data)]
		pb.pool.large.Put(&b)
	// tier -1: unmanaged, let GC collect
	}
}

// Get returns a PoolBuffer of exactly the requested size.
// The buffer is backed by a pooled slice from the appropriate tier.
func (bp *BufferPool) Get(size int) *PoolBuffer {
	var data []byte
	var tier int

	switch {
	case size <= tierSmall:
		ptr := bp.small.Get().(*[]byte)
		data = (*ptr)[:size]
		tier = 0
		bp.Hits.Add(1)
	case size <= tierMedium:
		ptr := bp.medium.Get().(*[]byte)
		data = (*ptr)[:size]
		tier = 1
		bp.Hits.Add(1)
	case size <= tierLarge:
		ptr := bp.large.Get().(*[]byte)
		data = (*ptr)[:size]
		tier = 2
		bp.Hits.Add(1)
	default:
		// Too large for pools — allocate directly.
		data = make([]byte, size)
		tier = -1
		bp.Misses.Add(1)
	}

	return &PoolBuffer{data: data, pool: bp, tier: tier}
}
