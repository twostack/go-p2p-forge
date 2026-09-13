package codec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"runtime"
	"testing"
	"time"
)

// A peer's length prefix is a claim, not an instruction to allocate. A frame
// that declares the maximum size and then delivers a few bytes must cost a
// few bytes, not ten megabytes per stream.
func TestReadFramePooled_LargeFrameCostsWhatArrives(t *testing.T) {
	var header [LengthPrefixSize]byte
	binary.BigEndian.PutUint32(header[:], MaxFrameSize)
	input := append(header[:], bytes.Repeat([]byte("x"), 100)...)

	pool := NewBufferPool()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := ReadFramePooled(bytes.NewReader(input), pool)
	runtime.ReadMemStats(&after)

	if err == nil {
		t.Fatal("expected a short-read error for a truncated frame")
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	if allocated > 1<<20 {
		t.Fatalf("declaring a %d byte frame and sending 100 allocated %d bytes; want well under 1 MB", MaxFrameSize, allocated)
	}
}

// The largest frame still has to arrive intact once it really is sent.
func TestReadFramePooled_LargeFrameRoundTrips(t *testing.T) {
	payload := bytes.Repeat([]byte{0xAB}, 3*1024*1024)
	var buf bytes.Buffer
	if err := WriteFrame(&buf, payload); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFramePooled(&buf, NewBufferPool())
	if err != nil {
		t.Fatal(err)
	}
	defer got.Release()
	if !bytes.Equal(got.Bytes(), payload) {
		t.Fatalf("large frame corrupted: got %d bytes", got.Len())
	}
}

// A peer that sends a length prefix and then goes quiet is abandoned after
// the idle timeout rather than holding the reader forever.
func TestReadFrameWithTimeout_StalledPeerIsAbandoned(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	var header [LengthPrefixSize]byte
	binary.BigEndian.PutUint32(header[:], 1024)
	go client.Write(header[:])

	start := time.Now()
	_, err := ReadFrameWithTimeout(server, 100*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected the read to fail on the idle deadline")
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("error = %v; want a deadline error", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("read took %v to give up on a stalled peer", elapsed)
	}
}

// The deadline is idle time, not total time: a peer that keeps sending, even
// slowly, is not cut off.
func TestReadFrameWithTimeout_SlowButLivePeerCompletes(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	payload := bytes.Repeat([]byte("y"), 4*chunkSize)
	go func() {
		var header [LengthPrefixSize]byte
		binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
		client.Write(header[:])
		for off := 0; off < len(payload); off += chunkSize {
			time.Sleep(60 * time.Millisecond) // under the idle window each time
			client.Write(payload[off : off+chunkSize])
		}
	}()

	// Total transfer takes ~240ms against a 150ms idle window.
	got, err := ReadFrameWithTimeout(server, 150*time.Millisecond)
	if err != nil {
		t.Fatalf("slow peer was cut off: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("payload corrupted")
	}
}

// A peer that stops reading the response is abandoned too.
func TestWriteFrameWithTimeout_StalledReaderIsAbandoned(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	start := time.Now()
	err := WriteFrameWithTimeout(server, bytes.Repeat([]byte("z"), 3*chunkSize), 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected the write to fail on the idle deadline")
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("error = %v; want a deadline error", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("write did not give up promptly")
	}
}
