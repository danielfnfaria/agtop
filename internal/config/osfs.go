package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// OSFS is the FS backed by the operating system.
type OSFS struct{}

var _ FS = OSFS{}

func (OSFS) IsDirectory(p string) bool {
	// Any failure (ENOENT, ENOTDIR, EACCES, ELOOP...) means "not a usable directory".
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func (OSFS) IsRegularFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// Realpath resolves symlinks and restores the on-disk case, so that two spellings of one
// directory on a case-insensitive filesystem compare equal.
func (OSFS) Realpath(p string) (string, error) {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	return canonicalCase(r), nil
}

func canonicalCase(p string) string {
	if !filepath.IsAbs(p) {
		return p
	}
	vol := filepath.VolumeName(p)
	rest := strings.TrimPrefix(p, vol)
	cur := vol + string(filepath.Separator)
	for _, name := range strings.Split(rest, string(filepath.Separator)) {
		if name == "" {
			continue
		}
		cur = filepath.Join(cur, matchCase(cur, name))
	}
	return cur
}

// matchCase returns the entry of dir that spells name as stored on disk.
func matchCase(dir, name string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return name
	}
	fold := ""
	for _, e := range entries {
		switch {
		case e.Name() == name:
			return name
		case strings.EqualFold(e.Name(), name):
			fold = e.Name()
		}
	}
	if fold != "" {
		return fold
	}
	return name
}

func (OSFS) ReadFile(p string) (string, bool, error) {
	b, err := os.ReadFile(p)
	if err == nil {
		return string(b), true, nil
	}
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.EISDIR) {
		return "", false, nil
	}
	return "", false, err // EACCES etc.: the loader reports it with the path
}

func (OSFS) ListDir(p string) []string {
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names
}

func (OSFS) MkdirAll(p string, perm fs.FileMode) error { return os.MkdirAll(p, perm) }

func (OSFS) CreateExcl(p, data string, perm fs.FileMode) error {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
