package discovery

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// ErrFileTooLarge is returned by FS.ReadFile when the file exceeds the byte cap. It never
// carries file content.
var ErrFileTooLarge = errors.New("file too large")

// Info is what discovery needs from a stat (following symlinks).
type Info struct {
	IsFile bool
	IsDir  bool
	Size   int64
	// ModTimeMs is the mtime in epoch ms.
	ModTimeMs float64
	// HasSignature is false when the fs reports neither size nor mtime.
	HasSignature bool
}

// Signature is "size:mtimeMs", to notice that a file changed without reading it.
func (i Info) Signature() (string, bool) {
	if !i.HasSignature {
		return "", false
	}
	return strconv.FormatInt(i.Size, 10) + ":" + strconv.FormatFloat(i.ModTimeMs, 'f', -1, 64), true
}

// FS is the subset of filesystem operations discovery uses, injectable for tests.
type FS interface {
	// ReadDir returns the entry names of a directory.
	ReadDir(p string) ([]string, error)
	// Stat follows symlinks.
	Stat(p string) (Info, error)
	// Realpath returns the canonical path (symlinks resolved).
	Realpath(p string) (string, error)
	// ReadFile returns the content as UTF-8 text, or ErrFileTooLarge past maxBytes.
	ReadFile(p string, maxBytes int) (string, error)
}

// OSFS is the FS backed by the operating system.
type OSFS struct{}

var _ FS = OSFS{}

func (OSFS) ReadDir(p string) ([]string, error) {
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names, nil
}

func (OSFS) Stat(p string) (Info, error) {
	st, err := os.Stat(p)
	if err != nil {
		return Info{}, err
	}
	return Info{
		IsFile:       st.Mode().IsRegular(),
		IsDir:        st.IsDir(),
		Size:         st.Size(),
		ModTimeMs:    float64(st.ModTime().UnixNano()) / 1e6,
		HasSignature: true,
	}, nil
}

func (OSFS) Realpath(p string) (string, error) { return filepath.EvalSymlinks(p) }

func (OSFS) ReadFile(p string, maxBytes int) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if st.Size() > int64(maxBytes) {
		return "", tooLarge(maxBytes)
	}
	return readCapped(f, maxBytes)
}

func tooLarge(maxBytes int) error {
	return fmt.Errorf("%w: more than %d bytes", ErrFileTooLarge, maxBytes)
}

// readCapped reads r up to maxBytes; one more byte means the file grew after the stat.
func readCapped(r io.Reader, maxBytes int) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, int64(maxBytes)+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxBytes {
		return "", tooLarge(maxBytes)
	}
	return string(b), nil
}
