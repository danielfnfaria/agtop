// Package tailer reads the lines appended to a file that only grows (JSONL) since the last
// read. It knows nothing about the format: it hands out raw lines.
package tailer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sync"
	"syscall"
	"unicode/utf8"

	"github.com/danielfnfaria/agtop/internal/errno"
)

const (
	// DefaultChunkSize is how many bytes are read at a time.
	DefaultChunkSize = 64 * 1024
	// DefaultMaxLineBytes is the longest line kept (bytes, without the "\n"); longer ones are
	// discarded and counted.
	DefaultMaxLineBytes = 4 * 1024 * 1024
)

// Stat is the minimum the tailer needs from a stat. Where the filesystem reports inode 0
// (no stable file id) a replaced file is not noticed by identity: only truncation (size below
// the offset) triggers a reset.
type Stat struct {
	Size int64
	Ino  uint64
	Dev  uint64
}

// File is an open file for positional reads.
type File interface {
	io.ReaderAt
	io.Closer
}

// FS is the subset of the operating system the tailer uses, injectable in tests.
type FS interface {
	Stat(path string) (Stat, error)
	Open(path string) (File, error)
}

// OSFS is the FS backed by the operating system.
type OSFS struct{}

var _ FS = OSFS{}

func (OSFS) Stat(path string) (Stat, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Stat{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Stat{Size: info.Size()}, nil
	}
	return Stat{Size: info.Size(), Ino: uint64(st.Ino), Dev: uint64(st.Dev)}, nil // the field types differ per OS
}

func (OSFS) Open(path string) (File, error) { return os.Open(path) }

// ErrInvalidOption is returned by New for a chunk size or line limit that is not positive.
var ErrInvalidOption = errors.New("tailer: invalid option")

// Option tunes a Tailer; without it the defaults apply.
type Option func(*Tailer)

// WithChunkSize sets how many bytes are read at a time (default DefaultChunkSize).
func WithChunkSize(n int) Option { return func(t *Tailer) { t.chunkSize = n } }

// WithMaxLineBytes sets the longest line kept (default DefaultMaxLineBytes).
func WithMaxLineBytes(n int) Option { return func(t *Tailer) { t.maxLineBytes = n } }

// WithFS sets the filesystem (default OSFS).
func WithFS(fs FS) Option { return func(t *Tailer) { t.fs = fs } }

// Result is what one Read found.
type Result struct {
	// Lines are the complete, non-empty lines, without the trailing "\n"/"\r", in file order.
	Lines []string
	// Reset: the file was truncated, replaced or removed; the consumer must drop its state.
	Reset bool
	// DroppedOversizedLines is how many lines exceeded MaxLineBytes in this read.
	DroppedOversizedLines int
}

// Error is a read failure. It carries only the path and the error code, never the original
// message: consumers read Code instead of parsing the message.
type Error struct {
	Path string
	Code string
}

func (e *Error) Error() string {
	return fmt.Sprintf("tailer: failed to read %s (%s)", e.Path, e.Code)
}

// ErrClosed is returned by Read after Close.
var ErrClosed = errors.New("tailer: closed")

// Tailer reads a file incrementally. It opens and closes the file on every Read.
type Tailer struct {
	path         string
	chunkSize    int
	maxLineBytes int
	fs           FS

	// mu serializes reads (and Close).
	mu       sync.Mutex
	closed   bool
	offset   int64
	identity *Stat
	partial  []byte
	// discarding drops the rest of an oversized line up to the next "\n".
	discarding bool
}

// New creates a Tailer for path; a chunk size or line limit that is not positive fails with
// ErrInvalidOption.
func New(path string, opts ...Option) (*Tailer, error) {
	t := &Tailer{path: path, chunkSize: DefaultChunkSize, maxLineBytes: DefaultMaxLineBytes, fs: OSFS{}}
	for _, o := range opts {
		o(t)
	}
	if t.chunkSize <= 0 {
		return nil, fmt.Errorf("%w: chunk size must be a positive integer", ErrInvalidOption)
	}
	if t.maxLineBytes <= 0 {
		return nil, fmt.Errorf("%w: max line bytes must be a positive integer", ErrInvalidOption)
	}
	if t.fs == nil {
		t.fs = OSFS{}
	}
	return t, nil
}

func (t *Tailer) clearState() {
	t.offset = 0
	t.identity = nil
	t.partial = nil
	t.discarding = false
}

func (t *Tailer) wrapError(err error) *Error {
	code := errno.Code(err)
	if code == "" {
		code = "UNKNOWN"
	}
	return &Error{Path: t.path, Code: code}
}

// Close waits for a pending Read; after that Read fails with ErrClosed.
func (t *Tailer) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	return nil
}

// Read returns what was appended since the last read. Concurrent calls are serialized.
func (t *Tailer) Read() (Result, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return Result{}, ErrClosed
	}
	var out Result

	st, err := t.fs.Stat(t.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return t.handleMissing(out), nil
		}
		return Result{}, t.wrapError(err)
	}

	replaced := t.identity != nil && (t.identity.Ino != st.Ino || t.identity.Dev != st.Dev)
	if replaced || st.Size < t.offset {
		t.clearState()
		out.Reset = true
	}
	t.identity = &st
	if st.Size == t.offset {
		return out, nil
	}

	f, err := t.fs.Open(t.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return t.handleMissing(out), nil
		}
		return Result{}, t.wrapError(err)
	}
	defer f.Close()

	buf := make([]byte, min(int64(t.chunkSize), st.Size-t.offset))
	for t.offset < st.Size {
		want := min(int64(len(buf)), st.Size-t.offset)
		n, err := f.ReadAt(buf[:want], t.offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return Result{}, t.wrapError(err)
		}
		if n == 0 {
			break // truncated while reading; the next stat notices
		}
		t.offset += int64(n)
		t.consume(buf[:n], &out)
	}
	return out, nil
}

// handleMissing: the file is gone; if there was state the consumer must drop it.
func (t *Tailer) handleMissing(out Result) Result {
	hadState := t.identity != nil || t.offset > 0 || len(t.partial) > 0
	t.clearState()
	out.Reset = out.Reset || hadState
	return out
}

// consume processes a chunk, accumulating complete lines in out.
func (t *Tailer) consume(chunk []byte, out *Result) {
	for start := 0; start < len(chunk); {
		nl := bytes.IndexByte(chunk[start:], '\n')
		end := len(chunk)
		if nl != -1 {
			end = start + nl
		}

		if t.discarding {
			if nl == -1 {
				return
			}
			t.discarding = false
			start = end + 1
			continue
		}

		if len(t.partial)+(end-start) > t.maxLineBytes {
			t.partial = nil
			out.DroppedOversizedLines++
			if nl == -1 {
				t.discarding = true
				return
			}
			start = end + 1
			continue
		}

		seg := chunk[start:end]
		if nl == -1 {
			// The chunk buffer is reused: keep a copy of the remainder.
			t.partial = append(t.partial, seg...)
			return
		}

		line := seg
		if len(t.partial) > 0 {
			line = append(t.partial, seg...)
			t.partial = nil
		}
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if len(line) > 0 {
			out.Lines = append(out.Lines, toValidUTF8(line))
		}
		start = end + 1
	}
}

// toValidUTF8 decodes like a lenient UTF-8 decoder: each invalid byte becomes U+FFFD.
func toValidUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb bytes.Buffer
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		sb.WriteRune(r)
		b = b[size:]
	}
	return sb.String()
}
