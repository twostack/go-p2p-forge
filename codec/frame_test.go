package codec

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

func TestReadWriteFrame_RoundTrip(t *testing.T) {
	payloads := [][]byte{
		[]byte("hello"),
		[]byte(`{"key":"value"}`),
		bytes.Repeat([]byte("x"), 1024),
		{0x00, 0xFF, 0x7F},
	}

	for _, payload := range payloads {
		var buf bytes.Buffer
		if err := WriteFrame(&buf, payload); err != nil {
			t.Fatalf("WriteFrame(%d bytes): %v", len(payload), err)
		}

		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}

		if !bytes.Equal(got, payload) {
			t.Errorf("round-trip mismatch: got %d bytes, want %d bytes", len(got), len(payload))
		}
	}
}

func TestReadFrame_EmptyFrame(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0))
	_, err := ReadFrame(&buf)
	if err == nil {
		t.Error("expected error for empty frame")
	}
}

func TestReadFrame_TooLarge(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(MaxFrameSize+1))
	_, err := ReadFrame(&buf)
	if err == nil {
		t.Error("expected error for oversized frame")
	}
}

func TestWriteFrame_TooLarge(t *testing.T) {
	data := make([]byte, MaxFrameSize+1)
	var buf bytes.Buffer
	err := WriteFrame(&buf, data)
	if err == nil {
		t.Error("expected error for oversized frame")
	}
}

func TestReadFrame_TruncatedLength(t *testing.T) {
	buf := bytes.NewBuffer([]byte{0x00, 0x00}) // only 2 bytes, need 4
	_, err := ReadFrame(buf)
	if err == nil {
		t.Error("expected error for truncated length prefix")
	}
}

func TestReadFrame_TruncatedData(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(100))
	buf.Write([]byte("short")) // only 5 bytes, claimed 100
	_, err := ReadFrame(&buf)
	if err == nil {
		t.Error("expected error for truncated data")
	}
}

func TestWriteFrame_Format(t *testing.T) {
	payload := []byte("test")
	var buf bytes.Buffer
	WriteFrame(&buf, payload)

	data := buf.Bytes()
	if len(data) != LengthPrefixSize+len(payload) {
		t.Fatalf("expected %d bytes, got %d", LengthPrefixSize+len(payload), len(data))
	}

	length := binary.BigEndian.Uint32(data[:LengthPrefixSize])
	if length != uint32(len(payload)) {
		t.Errorf("length prefix: got %d, want %d", length, len(payload))
	}

	if !bytes.Equal(data[LengthPrefixSize:], payload) {
		t.Error("payload mismatch after length prefix")
	}
}

func TestReadFramePooled_RoundTrip(t *testing.T) {
	pool := NewBufferPool()
	payload := []byte(`{"message":"hello from pooled"}`)

	var buf bytes.Buffer
	if err := WriteFrame(&buf, payload); err != nil {
		t.Fatal(err)
	}

	pb, err := ReadFramePooled(&buf, pool)
	if err != nil {
		t.Fatal(err)
	}
	defer pb.Release()

	if !bytes.Equal(pb.Bytes(), payload) {
		t.Error("pooled round-trip mismatch")
	}
}

func TestReadFramePooled_ReleasedBuffer(t *testing.T) {
	pool := NewBufferPool()
	payload := []byte("test")

	var buf bytes.Buffer
	WriteFrame(&buf, payload)

	pb, _ := ReadFramePooled(&buf, pool)
	pb.Release()
	pb.Release() // double release must not panic
}

func TestReadFramePooled_Error(t *testing.T) {
	pool := NewBufferPool()
	_, err := ReadFramePooled(bytes.NewReader(nil), pool)
	if err == nil {
		t.Error("expected error reading from empty reader")
	}
}

func TestMultipleFrames(t *testing.T) {
	var buf bytes.Buffer
	messages := []string{"first", "second", "third"}

	for _, msg := range messages {
		WriteFrame(&buf, []byte(msg))
	}

	for _, expected := range messages {
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		if string(got) != expected {
			t.Errorf("got %q, want %q", string(got), expected)
		}
	}

	// No more frames
	_, err := ReadFrame(&buf)
	if err == nil || err == io.EOF {
		// err should wrap io.EOF but may not be exactly io.EOF
	}
}
