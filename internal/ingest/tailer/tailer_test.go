package tailer

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
)

const (
	emoji   = "\U0001F600" // 4 bytes in UTF-8
	accents = "çãé"        // 2 bytes each
)

func tmpFile(t *testing.T, content string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func appendTo(t *testing.T, file string, data string) {
	t.Helper()
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(data); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func newTailer(t *testing.T, file string, opts ...Option) *Tailer {
	t.Helper()
	tl, err := New(file, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return tl
}

func read(t *testing.T, tl *Tailer) Result {
	t.Helper()
	r, err := tl.Read()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func expect(t *testing.T, got, want Result) {
	t.Helper()
	if len(got.Lines) == 0 && len(want.Lines) == 0 {
		got.Lines, want.Lines = nil, nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestTailerLineInTwoAppends(t *testing.T) {
	file := tmpFile(t, "")
	tl := newTailer(t, file)
	appendTo(t, file, `{"a":`)
	expect(t, read(t, tl), Result{})
	appendTo(t, file, "1}\n")
	expect(t, read(t, tl), Result{Lines: []string{`{"a":1}`}})
	expect(t, read(t, tl), Result{})
}

func TestTailerSkipsEmptyLines(t *testing.T) {
	tl := newTailer(t, tmpFile(t, "a\nb\n\n\nc\n"))
	expect(t, read(t, tl), Result{Lines: []string{"a", "b", "c"}})
}

func TestTailerMultibyteAcrossSmallChunks(t *testing.T) {
	l1 := `{"t":"` + emoji + accents + emoji + `"}`
	l2 := accents + "x" + emoji
	l3 := strings.Repeat(emoji, 5)
	tl := newTailer(t, tmpFile(t, l1+"\n"+l2+"\n"+l3+"\n"), WithChunkSize(7))
	expect(t, read(t, tl), Result{Lines: []string{l1, l2, l3}})
}

func TestTailerUTF8SplitBetweenWrites(t *testing.T) {
	line := "x" + emoji + "y"
	data := line + "\n"
	file := tmpFile(t, data[:3]) // cuts the emoji in the middle
	tl := newTailer(t, file, WithChunkSize(7))
	expect(t, read(t, tl), Result{})
	appendTo(t, file, data[3:])
	expect(t, read(t, tl), Result{Lines: []string{line}})
}

func TestTailerInvalidUTF8IsReplaced(t *testing.T) {
	tl := newTailer(t, tmpFile(t, "a\xffb\n"))
	expect(t, read(t, tl), Result{Lines: []string{"a�b"}})
}

func TestTailerCRLF(t *testing.T) {
	tl := newTailer(t, tmpFile(t, "abcdef\r\nxy\r\n"), WithChunkSize(7))
	expect(t, read(t, tl), Result{Lines: []string{"abcdef", "xy"}})
}

func TestTailerTruncationResets(t *testing.T) {
	file := tmpFile(t, "linha-um\nlinha-dois\n")
	tl := newTailer(t, file)
	expect(t, read(t, tl), Result{Lines: []string{"linha-um", "linha-dois"}})
	if err := os.WriteFile(file, []byte("novo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expect(t, read(t, tl), Result{Lines: []string{"novo"}, Reset: true})
	expect(t, read(t, tl), Result{})
}

func TestTailerTruncationDropsPartialLine(t *testing.T) {
	file := tmpFile(t, "a\nparcial-sem-fim")
	tl := newTailer(t, file)
	expect(t, read(t, tl), Result{Lines: []string{"a"}})
	if err := os.WriteFile(file, []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expect(t, read(t, tl), Result{Lines: []string{"b"}, Reset: true})
}

func TestTailerReplacementByRename(t *testing.T) {
	file := tmpFile(t, "a\n")
	tl := newTailer(t, file)
	expect(t, read(t, tl), Result{Lines: []string{"a"}})
	other := filepath.Join(filepath.Dir(file), "other.jsonl")
	if err := os.WriteFile(other, []byte("x\ny\nz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(other, file); err != nil {
		t.Fatal(err)
	}
	expect(t, read(t, tl), Result{Lines: []string{"x", "y", "z"}, Reset: true})
}

func TestTailerOversizedLines(t *testing.T) {
	big := strings.Repeat("G", 100)
	t.Run("dropped, neighbors intact", func(t *testing.T) {
		tl := newTailer(t, tmpFile(t, "ok1\n"+big+"\nok2\n"+big+big+"\nok3\n"), WithChunkSize(7), WithMaxLineBytes(50))
		expect(t, read(t, tl), Result{Lines: []string{"ok1", "ok2", "ok3"}, DroppedOversizedLines: 2})
	})
	t.Run("inside a single chunk", func(t *testing.T) {
		tl := newTailer(t, tmpFile(t, big+"\nok\n"), WithMaxLineBytes(50))
		expect(t, read(t, tl), Result{Lines: []string{"ok"}, DroppedOversizedLines: 1})
	})
	t.Run("arriving slowly, discarded up to the newline", func(t *testing.T) {
		file := tmpFile(t, "ok1\n")
		tl := newTailer(t, file, WithChunkSize(16), WithMaxLineBytes(50))
		expect(t, read(t, tl), Result{Lines: []string{"ok1"}})
		appendTo(t, file, strings.Repeat("G", 60))
		expect(t, read(t, tl), Result{DroppedOversizedLines: 1})
		appendTo(t, file, strings.Repeat("G", 60))
		expect(t, read(t, tl), Result{})
		appendTo(t, file, "GGG\nok2\n")
		expect(t, read(t, tl), Result{Lines: []string{"ok2"}})
	})
	t.Run("exactly maxLineBytes is kept", func(t *testing.T) {
		line := strings.Repeat("L", 50)
		tl := newTailer(t, tmpFile(t, line+"\n"), WithChunkSize(7), WithMaxLineBytes(50))
		expect(t, read(t, tl), Result{Lines: []string{line}})
	})
}

func TestTailerMissingFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "nao-existe.jsonl")
	tl := newTailer(t, file)
	expect(t, read(t, tl), Result{})
	if err := os.WriteFile(file, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expect(t, read(t, tl), Result{Lines: []string{"a"}})
}

func TestTailerRemovedAfterRead(t *testing.T) {
	file := tmpFile(t, "a\n")
	tl := newTailer(t, file)
	expect(t, read(t, tl), Result{Lines: []string{"a"}})
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	expect(t, read(t, tl), Result{Reset: true})
	expect(t, read(t, tl), Result{})
}

type fakeFile struct {
	data    []byte
	readErr error
	closed  *int
}

func (f fakeFile) ReadAt(p []byte, off int64) (int, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f fakeFile) Close() error {
	if f.closed != nil {
		*f.closed++
	}
	return nil
}

type fakeFS struct {
	stat func() (Stat, error)
	open func() (File, error)
}

func (f fakeFS) Stat(string) (Stat, error) { return f.stat() }
func (f fakeFS) Open(string) (File, error) { return f.open() }

func TestTailerOpenErrorHasPathAndCodeOnly(t *testing.T) {
	fake := fakeFS{
		stat: func() (Stat, error) { return Stat{Size: 10, Ino: 1, Dev: 1}, nil },
		open: func() (File, error) { return nil, fmt.Errorf("segredo do conteúdo: %w", syscall.EISDIR) },
	}
	tl := newTailer(t, "/x/dir", WithFS(fake))
	_, err := tl.Read()
	var te *Error
	if !errors.As(err, &te) {
		t.Fatalf("err = %v", err)
	}
	if err.Error() != "tailer: failed to read /x/dir (EISDIR)" || te.Path != "/x/dir" || te.Code != "EISDIR" {
		t.Errorf("err = %v", err)
	}
}

func TestErrorCodeDoesNotDependOnMessage(t *testing.T) {
	err := &Error{Path: "/x/y.jsonl", Code: "EIO"}
	if err.Code != "EIO" || err.Path != "/x/y.jsonl" {
		t.Errorf("err = %+v", err)
	}
}

func TestTailerReadErrorClosesHandle(t *testing.T) {
	closed := 0
	fake := fakeFS{
		stat: func() (Stat, error) { return Stat{Size: 10, Ino: 1, Dev: 1}, nil },
		open: func() (File, error) {
			return fakeFile{readErr: fmt.Errorf("segredo do conteúdo: %w", syscall.EIO), closed: &closed}, nil
		},
	}
	tl := newTailer(t, "/x/y.jsonl", WithFS(fake))
	_, err := tl.Read()
	if err == nil || err.Error() != "tailer: failed to read /x/y.jsonl (EIO)" || strings.Contains(err.Error(), "segredo") {
		t.Errorf("err = %v", err)
	}
	if closed != 1 {
		t.Errorf("closed = %d", closed)
	}
}

func TestTailerStatErrorDoesNotLeakAndQueueSurvives(t *testing.T) {
	fake := fakeFS{
		stat: func() (Stat, error) { return Stat{}, fmt.Errorf("segredo do conteúdo: %w", syscall.EACCES) },
		open: func() (File, error) { return nil, errors.New("não deveria abrir") },
	}
	tl := newTailer(t, "/x/y.jsonl", WithFS(fake))
	for i := 0; i < 2; i++ {
		if _, err := tl.Read(); err == nil || err.Error() != "tailer: failed to read /x/y.jsonl (EACCES)" {
			t.Errorf("read %d: err = %v", i, err)
		}
	}
}

func TestTailerFullWidthInodeIdentity(t *testing.T) {
	data := []byte("a\n")
	ino := uint64(1<<64 - 1)
	fake := fakeFS{
		stat: func() (Stat, error) { return Stat{Size: int64(len(data)), Ino: ino, Dev: 1}, nil },
		open: func() (File, error) { return fakeFile{data: data}, nil },
	}
	tl := newTailer(t, "/fake.jsonl", WithFS(fake))
	expect(t, read(t, tl), Result{Lines: []string{"a"}})
	ino-- // ids that differ only in the lowest bit of a 64-bit value still detect the swap
	expect(t, read(t, tl), Result{Lines: []string{"a"}, Reset: true})
}

func TestTailerConcurrentReadsNeitherDuplicateNorLoseLines(t *testing.T) {
	file := tmpFile(t, "")
	tl := newTailer(t, file, WithChunkSize(7))
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []Result
		errs    []error
	)
	reader := func() {
		defer wg.Done()
		r, err := tl.Read()
		mu.Lock()
		defer mu.Unlock()
		results = append(results, r)
		if err != nil {
			errs = append(errs, err)
		}
	}
	for i := 0; i < 50; i++ {
		line := fmt.Sprintf(`{"i":%d,"t":"%s%s"}`, i, accents, emoji)
		wg.Add(3)
		go reader()
		go reader()
		// Bursts of reads interleaved with writes that cut the line in half.
		appendTo(t, file, line[:5])
		go reader()
		appendTo(t, file, line[5:]+"\n")
	}
	wg.Wait()
	final := read(t, tl)
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	results = append(results, final)

	// The order across goroutines is not defined; nothing may be lost, duplicated or reset.
	seen := map[string]int{}
	for _, r := range results {
		if r.Reset {
			t.Error("unexpected reset")
		}
		for _, l := range r.Lines {
			seen[l]++
		}
	}
	if len(seen) != 50 {
		t.Errorf("got %d distinct lines, want 50", len(seen))
	}
	for l, n := range seen {
		if n != 1 {
			t.Errorf("line %q seen %d times", l, n)
		}
	}
}

func TestTailerSequentialLargeFile(t *testing.T) {
	line := `{"msg":"` + strings.Repeat("x", 200) + accents + emoji + `"}`
	const count = 20_000
	tl := newTailer(t, tmpFile(t, strings.Repeat(line+"\n", count)))
	r := read(t, tl)
	if len(r.Lines) != count {
		t.Fatalf("lines = %d", len(r.Lines))
	}
	for i, l := range r.Lines {
		if l != line {
			t.Fatalf("line %d differs", i)
		}
	}
}

func TestTailerClose(t *testing.T) {
	tl := newTailer(t, tmpFile(t, "a\n"))
	if err := tl.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := tl.Read(); !errors.Is(err, ErrClosed) {
		t.Errorf("err = %v", err)
	}
}

func TestNewValidatesOptions(t *testing.T) {
	if _, err := New("x", WithChunkSize(0)); !errors.Is(err, ErrInvalidOption) {
		t.Errorf("chunk size 0: err = %v", err)
	}
	if _, err := New("x", WithMaxLineBytes(-1)); !errors.Is(err, ErrInvalidOption) {
		t.Errorf("max line bytes -1: err = %v", err)
	}
}

func TestOSFSStat(t *testing.T) {
	file := tmpFile(t, "abc")
	st, err := OSFS{}.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	var real syscall.Stat_t
	if err := syscall.Stat(file, &real); err != nil {
		t.Fatal(err)
	}
	if st.Size != 3 || st.Ino != uint64(real.Ino) || st.Dev != uint64(real.Dev) || st.Ino == 0 {
		t.Errorf("st = %+v", st)
	}
}
