package adapterproto

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const MaxFrameSize uint32 = 16 << 20

var (
	ErrEmptyFrame    = errors.New("adapter protocol: empty frame")
	ErrFrameTooLarge = errors.New("adapter protocol: frame too large")
)

func WriteFrame(w io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("adapter protocol: encode frame: %w", err)
	}
	if uint64(len(payload)) > uint64(MaxFrameSize) {
		return fmt.Errorf(
			"%w: %d bytes exceeds %d",
			ErrFrameTooLarge,
			len(payload),
			MaxFrameSize,
		)
	}

	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(payload)))

	if err := writeAll(w, prefix[:]); err != nil {
		return fmt.Errorf("adapter protocol: write length: %w", err)
	}
	if err := writeAll(w, payload); err != nil {
		return fmt.Errorf("adapter protocol: write payload: %w", err)
	}
	return nil
}

func ReadFrame(r io.Reader, value any) error {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return fmt.Errorf("adapter protocol: read length: %w", err)
	}

	size := binary.BigEndian.Uint32(prefix[:])
	if size == 0 {
		return ErrEmptyFrame
	}
	if size > MaxFrameSize {
		return fmt.Errorf(
			"%w: %d bytes exceeds %d",
			ErrFrameTooLarge,
			size,
			MaxFrameSize,
		)
	}

	payload := make([]byte, int(size))
	if _, err := io.ReadFull(r, payload); err != nil {
		return fmt.Errorf("adapter protocol: read payload: %w", err)
	}
	if err := json.Unmarshal(payload, value); err != nil {
		return fmt.Errorf("adapter protocol: decode frame: %w", err)
	}
	return nil
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
