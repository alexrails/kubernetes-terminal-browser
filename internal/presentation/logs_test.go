package presentation

import (
	"strings"
	"testing"
)

func TestLogBufferBoundedAndSafe(t *testing.T) {
	b := NewLogBuffer()
	b.MaxBytes = 100
	b.MaxLines = 3
	_, _ = b.Write([]byte("one\ntwo\nthree\nfour\n"))
	_, _ = b.Write([]byte("\x1b[2J\x07\u202eevil\n"))
	s, _, truncated := b.Snapshot(0)
	if !truncated || strings.Contains(s, "one") || strings.ContainsRune(s, '\x1b') || strings.ContainsRune(s, '\u202e') {
		t.Fatalf("unsafe or unbounded: %q truncated=%t", s, truncated)
	}
}
func TestLogBufferSplitUTF8(t *testing.T) {
	b := NewLogBuffer()
	_, _ = b.Write([]byte{0xE2, 0x82})
	_, _ = b.Write([]byte{0xAC})
	s, _, _ := b.Snapshot(0)
	if s != "€" {
		t.Fatalf("got %q", s)
	}
}
