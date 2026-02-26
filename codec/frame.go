// Package codec provides wire-format encoding/decoding for libp2p stream protocols.
//
// The frame codec uses a 4-byte big-endian uint32 length prefix followed by the
// payload bytes. This is the same framing format used across all go-ricochet protocols.
package codec

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	// LengthPrefixSize is the number of bytes used for the frame length prefix.
	LengthPrefixSize = 4

	// MaxFrameSize is the maximum allowed frame size in bytes (10 MB).
	MaxFrameSize = 10 * 1024 * 1024
)

// ReadFrame reads a length-prefixed frame from r.
// It returns the frame payload or an error if the frame is malformed or too large.
func ReadFrame(r io.Reader) ([]byte, error) {
	var lenBuf [LengthPrefixSize]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, fmt.Errorf("read frame length: %w", err)
	}

	length := binary.BigEndian.Uint32(lenBuf[:])
	if length == 0 {
		return nil, fmt.Errorf("empty frame")
	}
	if length > MaxFrameSize {
		return nil, fmt.Errorf("frame too large: %d > %d", length, MaxFrameSize)
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("read frame data: %w", err)
	}

	return data, nil
}

// WriteFrame writes a length-prefixed frame to w.
func WriteFrame(w io.Writer, data []byte) error {
	if len(data) > MaxFrameSize {
		return fmt.Errorf("frame too large: %d > %d", len(data), MaxFrameSize)
	}

	var lenBuf [LengthPrefixSize]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))

	if _, err := w.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("write frame length: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write frame data: %w", err)
	}

	return nil
}

// ReadFramePooled reads a length-prefixed frame using a pooled buffer.
// The caller MUST call PoolBuffer.Release() when done with the returned buffer.
func ReadFramePooled(r io.Reader, pool *BufferPool) (*PoolBuffer, error) {
	var lenBuf [LengthPrefixSize]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, fmt.Errorf("read frame length: %w", err)
	}

	length := binary.BigEndian.Uint32(lenBuf[:])
	if length == 0 {
		return nil, fmt.Errorf("empty frame")
	}
	if length > MaxFrameSize {
		return nil, fmt.Errorf("frame too large: %d > %d", length, MaxFrameSize)
	}

	buf := pool.Get(int(length))
	if _, err := io.ReadFull(r, buf.Bytes()); err != nil {
		buf.Release()
		return nil, fmt.Errorf("read frame data: %w", err)
	}

	return buf, nil
}
