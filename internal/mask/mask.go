// Package mask provides a streaming io.WriteCloser that replaces secret values
// with "***" as bytes flow through it. It is used to keep declared secrets out
// of latchet's per-job log files and streamed step output.
//
// A secret can be split across two Write calls (the runtime streams output in
// arbitrary chunks), so the writer holds back a tail of up to maxLen-1 bytes —
// the longest possible partial prefix of any secret — and only flushes it once
// it knows the bytes don't begin a secret. Close flushes whatever remains.
package mask

import (
	"bytes"
	"io"
	"sort"
	"strings"
)

type writer struct {
	w       io.Writer
	secrets []string // non-empty, sorted longest-first
	maxLen  int
	buf     []byte
}

// New wraps w so that every occurrence of any secret value is replaced with
// "***". Empty secrets are ignored. When no non-empty secrets remain, New
// returns a passthrough whose Close is a no-op and which never buffers — so
// the common (no-secrets) path is free. Close never closes the underlying
// writer; it only flushes the held tail.
func New(w io.Writer, secrets []string) io.WriteCloser {
	uniq := map[string]bool{}
	var s []string
	for _, v := range secrets {
		if v != "" && !uniq[v] {
			uniq[v] = true
			s = append(s, v)
		}
	}
	if len(s) == 0 {
		return passthrough{w}
	}
	// Longest first so an overlapping/shorter secret can't pre-empt a longer
	// match during replacement.
	sort.Slice(s, func(i, j int) bool { return len(s[i]) > len(s[j]) })
	return &writer{w: w, secrets: s, maxLen: len(s[0])}
}

func (m *writer) Write(p []byte) (int, error) {
	m.buf = append(m.buf, p...)
	m.replace()
	hold := m.holdLen()
	flush := m.buf[:len(m.buf)-hold]
	if len(flush) > 0 {
		if _, err := m.w.Write(flush); err != nil {
			// Keep the unflushed tail; report the original length as consumed.
			m.buf = append([]byte(nil), m.buf[len(m.buf)-hold:]...)
			return len(p), err
		}
	}
	m.buf = append([]byte(nil), m.buf[len(m.buf)-hold:]...)
	return len(p), nil
}

// Close flushes the held tail (after a final replace) without closing w.
func (m *writer) Close() error {
	m.replace()
	if len(m.buf) == 0 {
		return nil
	}
	_, err := m.w.Write(m.buf)
	m.buf = nil
	return err
}

func (m *writer) replace() {
	for _, s := range m.secrets {
		m.buf = bytes.ReplaceAll(m.buf, []byte(s), []byte("***"))
	}
}

// holdLen returns the length of the longest suffix of buf that is a proper
// prefix of some secret — those bytes might be the start of a secret completed
// by the next Write, so they must not be flushed yet.
func (m *writer) holdLen() int {
	max := m.maxLen - 1
	if max > len(m.buf) {
		max = len(m.buf)
	}
	for l := max; l >= 1; l-- {
		suf := string(m.buf[len(m.buf)-l:])
		for _, s := range m.secrets {
			if len(s) > l && strings.HasPrefix(s, suf) {
				return l
			}
		}
	}
	return 0
}

type passthrough struct{ w io.Writer }

func (p passthrough) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p passthrough) Close() error                { return nil }
