package adapterproto

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	node := uint32(7)
	want := Request{
		Version: Version,
		ID:      42,
		Op:      OpDeliver,
		Node:    &node,
		Message: &Message{
			From:    3,
			To:      7,
			Kind:    9,
			Value:   123456789,
			Payload: []byte{0, 1, 2, 127, 255},
		},
	}

	var wire bytes.Buffer
	if err := WriteFrame(&wire, want); err != nil {
		t.Fatalf("WriteFrame() failed: %v", err)
	}

	encoded := wire.Bytes()
	if len(encoded) < 4 {
		t.Fatalf("encoded frame has %d bytes, want at least 4", len(encoded))
	}
	if got, wantSize := binary.BigEndian.Uint32(encoded[:4]), uint32(len(encoded)-4); got != wantSize {
		t.Fatalf("length prefix = %d, want %d", got, wantSize)
	}

	var got Request
	if err := ReadFrame(&wire, &got); err != nil {
		t.Fatalf("ReadFrame() failed: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}

func TestReadFrameRejectsInvalidInput(t *testing.T) {
	oversized := make([]byte, 4)
	binary.BigEndian.PutUint32(oversized, MaxFrameSize+1)

	tests := []struct {
		name    string
		wire    []byte
		wantErr error
	}{
		{
			name:    "truncated length",
			wire:    []byte{0, 0},
			wantErr: io.ErrUnexpectedEOF,
		},
		{
			name:    "empty frame",
			wire:    []byte{0, 0, 0, 0},
			wantErr: ErrEmptyFrame,
		},
		{
			name:    "oversized frame",
			wire:    oversized,
			wantErr: ErrFrameTooLarge,
		},
		{
			name:    "truncated payload",
			wire:    rawFrame(8, []byte(`{}`)),
			wantErr: io.ErrUnexpectedEOF,
		},
		{
			name: "invalid JSON",
			wire: rawFrame(1, []byte(`{`)),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var response Response
			err := ReadFrame(bytes.NewReader(test.wire), &response)
			if err == nil {
				t.Fatal("ReadFrame() succeeded, want error")
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("ReadFrame() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestWriteFrameCompletesShortWrites(t *testing.T) {
	writer := &chunkWriter{limit: 3}
	want := Response{
		Version: Version,
		ID:      17,
		Op:      OpDrain,
		Messages: []Message{
			{From: 1, To: 2, Kind: 3, Value: 4, Payload: []byte("token")},
		},
	}

	if err := WriteFrame(writer, want); err != nil {
		t.Fatalf("WriteFrame() failed: %v", err)
	}

	var got Response
	if err := ReadFrame(bytes.NewReader(writer.Bytes()), &got); err != nil {
		t.Fatalf("ReadFrame() failed: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("short-write round trip = %#v, want %#v", got, want)
	}
}

func TestWriteFrameRejectsZeroWrite(t *testing.T) {
	err := WriteFrame(zeroWriter{}, Request{
		Version: Version,
		ID:      1,
		Op:      OpHello,
	})
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("WriteFrame() error = %v, want %v", err, io.ErrShortWrite)
	}
}

func rawFrame(size uint32, payload []byte) []byte {
	frame := make([]byte, 4, 4+len(payload))
	binary.BigEndian.PutUint32(frame, size)
	return append(frame, payload...)
}

type chunkWriter struct {
	bytes.Buffer
	limit int
}

func (w *chunkWriter) Write(data []byte) (int, error) {
	if len(data) > w.limit {
		data = data[:w.limit]
	}
	return w.Buffer.Write(data)
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) {
	return 0, nil
}
