// Package codec provides wire-format encoding/decoding for libp2p stream protocols.
//
// The frame codec uses a 4-byte big-endian uint32 length prefix followed by the
// payload bytes. This is the same framing format used across all go-ricochet protocols.
package codec

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

const (
	// LengthPrefixSize is the number of bytes used for the frame length prefix.
	LengthPrefixSize = 4

	// MaxFrameSize is the maximum allowed frame size in bytes (10 MB).
	MaxFrameSize = 10 * 1024 * 1024

	// chunkSize is how much of a frame body is read or written between
	// deadline refreshes. It bounds how long a peer can stall before the
	// idle timeout notices, and it is the unit by which a large frame's
	// buffer grows, so a peer that declares a big frame and sends nothing
	// costs nothing.
	chunkSize = 64 * 1024

	// largeFrameInitialCap is the starting capacity for frames too large for
	// the buffer pool. It grows as bytes actually arrive.
	largeFrameInitialCap = 256 * 1024
)

// readDeadliner is the subset of net.Conn / network.Stream needed to bound a
// read. bytes.Buffer and plain io.Readers do not implement it, and for those
// the idle timeout is simply not applied.
type readDeadliner interface {
	SetReadDeadline(t time.Time) error
}

type writeDeadliner interface {
	SetWriteDeadline(t time.Time) error
}

// ReadFrame reads a length-prefixed frame from r.
// It returns the frame payload or an error if the frame is malformed or too large.
func ReadFrame(r io.Reader) ([]byte, error) {
	return ReadFrameWithTimeout(r, 0)
}

// ReadFrameWithTimeout reads a length-prefixed frame from r, requiring the
// peer to make progress at least every idle interval. The deadline is
// refreshed after each chunk received, so a slow but live peer is not cut
// off, while a peer that declares a frame and then goes quiet is. A zero
// idle disables the deadline, as does a reader without SetReadDeadline.
func ReadFrameWithTimeout(r io.Reader, idle time.Duration) ([]byte, error) {
	length, err := readFrameLength(r, idle)
	if err != nil {
		return nil, err
	}
	return readLargeBody(r, int(length), idle)
}

// WriteFrame writes a length-prefixed frame to w.
func WriteFrame(w io.Writer, data []byte) error {
	return WriteFrameWithTimeout(w, data, 0)
}

// WriteFrameWithTimeout writes a length-prefixed frame to w in chunks,
// refreshing the write deadline before each so a peer that stops reading is
// abandoned after idle rather than holding the writer for the whole frame.
// A zero idle disables the deadline, as does a writer without SetWriteDeadline.
func WriteFrameWithTimeout(w io.Writer, data []byte, idle time.Duration) error {
	if len(data) > MaxFrameSize {
		return fmt.Errorf("frame too large: %d > %d", len(data), MaxFrameSize)
	}

	var lenBuf [LengthPrefixSize]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))

	armWrite(w, idle)
	if _, err := w.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("write frame length: %w", err)
	}
	for off := 0; off < len(data); off += chunkSize {
		end := min(off+chunkSize, len(data))
		armWrite(w, idle)
		if _, err := w.Write(data[off:end]); err != nil {
			return fmt.Errorf("write frame data: %w", err)
		}
	}

	return nil
}

// ReadFramePooled reads a length-prefixed frame using a pooled buffer.
// The caller MUST call PoolBuffer.Release() when done with the returned buffer.
func ReadFramePooled(r io.Reader, pool *BufferPool) (*PoolBuffer, error) {
	return ReadFramePooledWithTimeout(r, pool, 0)
}

// ReadFramePooledWithTimeout is ReadFramePooled with the progress deadline of
// ReadFrameWithTimeout.
//
// Frames that fit a pool tier are read into a pooled buffer, which is already
// allocated and is returned to the pool when released. Frames larger than the
// biggest tier are read into a buffer that grows as bytes arrive: the length
// prefix is the peer's claim, not a reason to allocate ten megabytes.
func ReadFramePooledWithTimeout(r io.Reader, pool *BufferPool, idle time.Duration) (*PoolBuffer, error) {
	length, err := readFrameLength(r, idle)
	if err != nil {
		return nil, err
	}

	if int(length) > tierLarge {
		data, err := readLargeBody(r, int(length), idle)
		if err != nil {
			return nil, err
		}
		return &PoolBuffer{data: data, pool: pool, tier: -1}, nil
	}

	buf := pool.Get(int(length))
	if err := readBodyInto(r, buf.Bytes(), idle); err != nil {
		buf.Release()
		return nil, err
	}
	return buf, nil
}

func readFrameLength(r io.Reader, idle time.Duration) (uint32, error) {
	var lenBuf [LengthPrefixSize]byte
	armRead(r, idle)
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return 0, fmt.Errorf("read frame length: %w", err)
	}

	length := binary.BigEndian.Uint32(lenBuf[:])
	if length == 0 {
		return 0, fmt.Errorf("empty frame")
	}
	if length > MaxFrameSize {
		return 0, fmt.Errorf("frame too large: %d > %d", length, MaxFrameSize)
	}
	return length, nil
}

// readBodyInto fills dst from r a chunk at a time, refreshing the deadline
// before each chunk.
func readBodyInto(r io.Reader, dst []byte, idle time.Duration) error {
	for off := 0; off < len(dst); off += chunkSize {
		end := min(off+chunkSize, len(dst))
		armRead(r, idle)
		if _, err := io.ReadFull(r, dst[off:end]); err != nil {
			return fmt.Errorf("read frame data: %w", err)
		}
	}
	return nil
}

// readLargeBody reads length bytes into a buffer that grows with what has
// actually arrived, so memory pinned by a stream tracks bytes received rather
// than bytes promised.
func readLargeBody(r io.Reader, length int, idle time.Duration) ([]byte, error) {
	data := make([]byte, 0, min(length, largeFrameInitialCap))
	var chunk [chunkSize]byte
	for len(data) < length {
		want := min(chunkSize, length-len(data))
		armRead(r, idle)
		n, err := io.ReadFull(r, chunk[:want])
		if err != nil {
			return nil, fmt.Errorf("read frame data: %w", err)
		}
		data = append(data, chunk[:n]...)
	}
	return data, nil
}

func armRead(r io.Reader, idle time.Duration) {
	if idle <= 0 {
		return
	}
	if d, ok := r.(readDeadliner); ok {
		_ = d.SetReadDeadline(time.Now().Add(idle))
	}
}

func armWrite(w io.Writer, idle time.Duration) {
	if idle <= 0 {
		return
	}
	if d, ok := w.(writeDeadliner); ok {
		_ = d.SetWriteDeadline(time.Now().Add(idle))
	}
}
