package ui

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	captureMaxEntries = 200
	captureMaxBytes   = 64 * 1024
	captureNotify     = 250 * time.Millisecond
)

// heldOutput keeps what is written while the full screen is on: a count of the messages (each
// line, plus a trailing partial one) and the first ones up to the limits. Once released, writes
// go straight to the passthrough writer.
type heldOutput struct {
	mu      sync.Mutex
	kept    []string
	bytes   int
	total   int
	partial []byte
	notify  func(int)
	pending bool
	through io.Writer
}

func (h *heldOutput) Write(b []byte) (int, error) {
	h.mu.Lock()
	if h.through != nil {
		w := h.through
		h.mu.Unlock()
		return w.Write(b)
	}
	h.partial = append(h.partial, b...)
	for {
		i := bytes.IndexByte(h.partial, '\n')
		if i < 0 {
			break
		}
		h.keep(string(h.partial[:i+1]))
		h.partial = h.partial[i+1:]
	}
	if h.notify != nil && !h.pending {
		h.pending = true
		time.AfterFunc(captureNotify, func() {
			h.mu.Lock()
			h.pending = false
			n, f := h.count(), h.notify
			h.mu.Unlock()
			if f != nil {
				f(n)
			}
		})
	}
	h.mu.Unlock()
	return len(b), nil
}

func (h *heldOutput) keep(msg string) {
	h.total++
	if len(h.kept) < captureMaxEntries && h.bytes+len(msg) <= captureMaxBytes {
		h.kept = append(h.kept, msg)
		h.bytes += len(msg)
	}
}

// count is the number of messages so far, the trailing partial one included.
func (h *heldOutput) count() int {
	if len(h.partial) > 0 {
		return h.total + 1
	}
	return h.total
}

func (h *heldOutput) onCount(f func(int)) {
	h.mu.Lock()
	h.notify = f
	h.mu.Unlock()
}

// text is what goes to stderr after the screen is restored ("" when nothing was written).
func (h *heldOutput) text() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.textLocked()
}

func (h *heldOutput) textLocked() string {
	h.notify = nil
	if len(h.partial) > 0 {
		h.keep(string(h.partial))
		h.partial = nil
	}
	if h.total == 0 {
		return ""
	}
	body := strings.Join(h.kept, "")
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	out := fmt.Sprintf("agtop: %d %s held during full screen:\n", h.total, plural(h.total, "message", "messages")) + body
	if dropped := h.total - len(h.kept); dropped > 0 {
		out += fmt.Sprintf("agtop: %d %s dropped (buffer limit)\n", dropped, plural(dropped, "message", "messages"))
	}
	return out
}

// passthrough returns the held text and sends later writes to w.
func (h *heldOutput) passthrough(w io.Writer) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.textLocked()
	h.through = w
	return out
}

// stderrCapture redirects os.Stderr and the log package to a pipe while the screen is full.
type stderrCapture struct {
	*heldOutput
	orig    *os.File
	w       *os.File
	drained chan struct{}
	once    sync.Once
	out     string
}

func captureStderr() *stderrCapture {
	c := &stderrCapture{heldOutput: &heldOutput{}, orig: os.Stderr}
	r, w, err := os.Pipe()
	if err != nil {
		return c
	}
	c.w = w
	c.drained = make(chan struct{})
	os.Stderr = w
	log.SetOutput(w)
	go func() {
		defer close(c.drained)
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				_, _ = c.Write(buf[:n])
			}
			if err != nil {
				_ = r.Close()
				return
			}
		}
	}()
	return c
}

// release restores os.Stderr and returns the held text (once). With quiet (every goroutine that
// may write to the pipe is gone) the pipe is closed and drained first; otherwise it stays open
// and what still arrives goes to the restored stderr.
func (c *stderrCapture) release(quiet bool) string {
	c.once.Do(func() {
		if c.w == nil {
			c.out = c.text()
			return
		}
		os.Stderr = c.orig
		log.SetOutput(c.orig)
		if !quiet {
			c.out = c.passthrough(c.orig)
			return
		}
		_ = c.w.Close()
		<-c.drained
		c.out = c.text()
	})
	return c.out
}
