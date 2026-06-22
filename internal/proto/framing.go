package proto

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// MaxFrameSize bounds a single JSON frame to prevent allocation bombs.
const MaxFrameSize = 1 << 20 // 1 MiB

// WriteFrame JSON-encodes v then writes a uint32 big-endian length prefix
// followed by the payload.
func WriteFrame(w io.Writer, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal frame: %w", err)
	}
	if len(payload) > MaxFrameSize {
		return fmt.Errorf("frame too large: %d > %d", len(payload), MaxFrameSize)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

// ReadFrame reads a length-prefixed JSON frame into v. Returns io.EOF cleanly
// if the stream ends before any bytes are read.
func ReadFrame(r io.Reader, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 {
		return errors.New("zero-length frame")
	}
	if n > MaxFrameSize {
		return fmt.Errorf("frame too large: %d > %d", n, MaxFrameSize)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	return json.Unmarshal(buf, v)
}
