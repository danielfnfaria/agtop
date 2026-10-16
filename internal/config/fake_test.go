package config

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
)

// fakeFS is an in-memory FS: dirs are existing directories (parents are implied), links maps a
// symlink path to its target and files maps a path to its content.
type fakeFS struct {
	dirs  map[string]bool
	links map[string]string
	files map[string]string
}

func newFakeFS(dirs []string, links map[string]string, files map[string]string) *fakeFS {
	f := &fakeFS{dirs: map[string]bool{}, links: links, files: map[string]string{}}
	for k, v := range files {
		f.files[k] = v
	}
	for _, d := range dirs {
		f.addDir(d)
	}
	return f
}

func (f *fakeFS) addDir(d string) {
	for !f.dirs[d] {
		f.dirs[d] = true
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
}

func (f *fakeFS) real(q string) string {
	q = filepath.Clean(q)
	for link, target := range f.links {
		if q == link || strings.HasPrefix(q, link+"/") {
			return f.real(target + q[len(link):])
		}
	}
	return q
}

func (f *fakeFS) IsDirectory(q string) bool { return f.dirs[f.real(q)] }

func (f *fakeFS) IsRegularFile(q string) bool { _, ok := f.files[f.real(q)]; return ok }

func (f *fakeFS) Realpath(q string) (string, error) {
	r := f.real(q)
	if !f.dirs[r] {
		return "", errors.New("ENOENT: " + q)
	}
	return r, nil
}

func (f *fakeFS) ReadFile(q string) (string, bool, error) {
	s, ok := f.files[q]
	return s, ok, nil
}

func (f *fakeFS) ListDir(q string) []string {
	base := f.real(q)
	var names []string
	for d := range f.dirs {
		if d != base && filepath.Dir(d) == base {
			names = append(names, filepath.Base(d))
		}
	}
	return names
}

func (f *fakeFS) MkdirAll(q string, _ fs.FileMode) error { f.addDir(q); return nil }

func (f *fakeFS) CreateExcl(q, data string, _ fs.FileMode) error {
	if _, ok := f.files[q]; ok || f.dirs[q] {
		return fs.ErrExist
	}
	f.files[q] = data
	return nil
}

// readErrFS fails every read.
type readErrFS struct{ *fakeFS }

func (readErrFS) ReadFile(string) (string, bool, error) { return "", false, errors.New("EACCES") }
