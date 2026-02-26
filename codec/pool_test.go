package codec

import (
	"testing"
)

func TestBufferPool_SmallTier(t *testing.T) {
	pool := NewBufferPool()
	buf := pool.Get(100)

	if buf.Len() != 100 {
		t.Errorf("expected len 100, got %d", buf.Len())
	}
	if cap(buf.Bytes()) < 100 {
		t.Errorf("expected cap >= 100, got %d", cap(buf.Bytes()))
	}
	if buf.tier != 0 {
		t.Errorf("expected small tier (0), got %d", buf.tier)
	}

	buf.Release()
}

func TestBufferPool_MediumTier(t *testing.T) {
	pool := NewBufferPool()
	buf := pool.Get(tierSmall + 1)

	if buf.tier != 1 {
		t.Errorf("expected medium tier (1), got %d", buf.tier)
	}
	if buf.Len() != tierSmall+1 {
		t.Errorf("expected len %d, got %d", tierSmall+1, buf.Len())
	}

	buf.Release()
}

func TestBufferPool_LargeTier(t *testing.T) {
	pool := NewBufferPool()
	buf := pool.Get(tierMedium + 1)

	if buf.tier != 2 {
		t.Errorf("expected large tier (2), got %d", buf.tier)
	}

	buf.Release()
}

func TestBufferPool_Unmanaged(t *testing.T) {
	pool := NewBufferPool()
	size := tierLarge + 1
	buf := pool.Get(size)

	if buf.tier != -1 {
		t.Errorf("expected unmanaged tier (-1), got %d", buf.tier)
	}
	if buf.Len() != size {
		t.Errorf("expected len %d, got %d", size, buf.Len())
	}

	buf.Release() // should not panic
}

func TestPoolBuffer_DoubleRelease(t *testing.T) {
	pool := NewBufferPool()
	buf := pool.Get(100)
	buf.Release()
	buf.Release() // must not panic
}

func TestBufferPool_Metrics(t *testing.T) {
	pool := NewBufferPool()

	// Small, medium, large should be hits
	pool.Get(100).Release()
	pool.Get(tierSmall + 1).Release()
	pool.Get(tierMedium + 1).Release()

	if pool.Hits.Load() != 3 {
		t.Errorf("expected 3 hits, got %d", pool.Hits.Load())
	}

	// Unmanaged should be a miss
	pool.Get(tierLarge + 1).Release()
	if pool.Misses.Load() != 1 {
		t.Errorf("expected 1 miss, got %d", pool.Misses.Load())
	}
}

func TestBufferPool_Reuse(t *testing.T) {
	pool := NewBufferPool()

	// Get and release a buffer
	buf1 := pool.Get(100)
	// Write some data to verify it's a real buffer
	copy(buf1.Bytes(), []byte("hello"))
	buf1.Release()

	// Get another buffer of the same tier
	buf2 := pool.Get(50)
	if buf2.Len() != 50 {
		t.Errorf("expected len 50, got %d", buf2.Len())
	}
	buf2.Release()
}

func TestBufferPool_ExactTierBoundaries(t *testing.T) {
	pool := NewBufferPool()

	tests := []struct {
		size int
		tier int
	}{
		{1, 0},
		{tierSmall, 0},
		{tierSmall + 1, 1},
		{tierMedium, 1},
		{tierMedium + 1, 2},
		{tierLarge, 2},
		{tierLarge + 1, -1},
	}

	for _, tt := range tests {
		buf := pool.Get(tt.size)
		if buf.tier != tt.tier {
			t.Errorf("size %d: expected tier %d, got %d", tt.size, tt.tier, buf.tier)
		}
		buf.Release()
	}
}
