package proto

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	in := Request{URL: "https://example.com", LoopbackPorts: []int{8085, 9090}, AuthToken: "abc"}
	if err := WriteFrame(&buf, in); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	var out Request
	if err := ReadFrame(&buf, &out); err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if out.URL != in.URL || out.AuthToken != in.AuthToken || len(out.LoopbackPorts) != 2 {
		t.Fatalf("round trip mismatch: %+v vs %+v", out, in)
	}
}

func TestReadFrameRejectsOversize(t *testing.T) {
	var buf bytes.Buffer
	// Length prefix claims MaxFrameSize+1 bytes.
	buf.Write([]byte{0x00, 0x10, 0x00, 0x01})
	buf.WriteString(strings.Repeat("x", 16))
	var out Request
	err := ReadFrame(&buf, &out)
	if err == nil {
		t.Fatal("expected oversize error, got nil")
	}
}

func TestReadFrameEOFOnEmpty(t *testing.T) {
	var out Request
	err := ReadFrame(&bytes.Buffer{}, &out)
	if err != io.EOF && err == nil {
		t.Fatalf("expected EOF or error, got nil")
	}
}
