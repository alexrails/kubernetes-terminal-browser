package presentation

import (
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// SafeText renders all terminal controls inert, including C1 and bidi controls.
func SafeText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return '�'
		}
		return r
	}, s)
}

type LogBuffer struct {
	mu                       sync.Mutex
	lines                    []string
	partial                  []byte
	pending                  []byte
	size, MaxLines, MaxBytes int
	revision                 uint64
	truncated                bool
}

func NewLogBuffer() *LogBuffer { return &LogBuffer{MaxLines: 10000, MaxBytes: 10 << 20} }
func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	p = append(b.pending, p...)
	b.pending = nil
	// Decode incrementally so split UTF-8 sequences survive chunk boundaries.
	var text strings.Builder
	for len(p) > 0 {
		if !utf8.FullRune(p) {
			b.pending = append(b.pending, p...)
			break
		}
		r, size := utf8.DecodeRune(p)
		text.WriteRune(r)
		p = p[size:]
	}
	clean := SafeText(text.String())
	for _, r := range clean {
		if r == '\n' {
			b.lines = append(b.lines, string(b.partial))
			b.size += len(b.partial) + 1
			b.partial = b.partial[:0]
		} else {
			b.partial = utf8.AppendRune(b.partial, r)
			if len(b.partial) >= 64<<10 {
				b.lines = append(b.lines, string(b.partial)+" [long line split]")
				b.size += len(b.lines[len(b.lines)-1]) + 1
				b.partial = b.partial[:0]
				b.truncated = true
			}
		}
	}
	for len(b.lines) > 0 && (len(b.lines)+1 > b.MaxLines || b.size+len(b.partial) > b.MaxBytes) {
		b.size -= len(b.lines[0]) + 1
		b.lines[0] = ""
		b.lines = b.lines[1:]
		b.truncated = true
	}
	if len(b.partial) > b.MaxBytes {
		b.partial = b.partial[len(b.partial)-b.MaxBytes:]
		b.truncated = true
	}
	b.revision++
	return n, nil
}
func (b *LogBuffer) Snapshot(previous uint64) (string, uint64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if previous == b.revision {
		return "", previous, b.truncated
	}
	return strings.Join(append(append([]string(nil), b.lines...), string(b.partial)), "\n"), b.revision, b.truncated
}
