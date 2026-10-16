//go:build darwin

package proc

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseProcArgs2(t *testing.T) {
	b := make([]byte, 4)
	binary.NativeEndian.PutUint32(b, 3)
	b = append(b, "/usr/bin/node\x00"...)
	b = append(b, 0, 0, 0) // NUL padding before argv
	b = append(b, "/usr/bin/node\x00"...)
	b = append(b, "/opt/claude-code/cli.js\x00"...)
	b = append(b, "--foo\x00"...)
	b = append(b, "PATH=/usr/bin\x00"...)

	want := []string{"/usr/bin/node", "/opt/claude-code/cli.js", "--foo"}
	if got := parseProcArgs2(b); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseProcArgs2Malformed(t *testing.T) {
	if got := parseProcArgs2(nil); got != nil {
		t.Errorf("nil = %q", got)
	}
	if got := parseProcArgs2([]byte{1, 2, 3}); got != nil {
		t.Errorf("short = %q", got)
	}
	zero := make([]byte, 4)
	if got := parseProcArgs2(zero); got != nil {
		t.Errorf("argc 0 = %q", got)
	}
}

func TestCString(t *testing.T) {
	if got := cString([]byte{'f', 'o', 'o', 0, 'x'}); got != "foo" {
		t.Errorf("got %q", got)
	}
	if got := cString([]byte("bar")); got != "bar" {
		t.Errorf("got %q", got)
	}
}

func TestParseVnodePathInfo(t *testing.T) {
	buf := make([]byte, vnodePathInfoSize)
	copy(buf[vnodeInfoSize:], "/Users/u/proj\x00")
	copy(buf[vnodeInfoPathSize+vnodeInfoSize:], "/\x00")
	if got := parseVnodePathInfo(buf); got != "/Users/u/proj" {
		t.Errorf("cwd = %q", got)
	}
	if got := parseVnodePathInfo(buf[:10]); got != "" {
		t.Errorf("short buffer = %q", got)
	}
	if got := exeFromProcArgs2(append([]byte{1, 0, 0, 0}, "/opt/opencode\x00argv\x00"...)); got != "/opt/opencode" {
		t.Errorf("exe = %q", got)
	}
	if got := exeFromProcArgs2([]byte{1, 0}); got != "" {
		t.Errorf("short args = %q", got)
	}
}

func TestListFindsSelf(t *testing.T) {
	self, err := Lookup(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	procs, err := List(self.Name)
	if err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	wd, _ = filepath.EvalSymlinks(wd)
	for _, p := range procs {
		if p.PID == os.Getpid() {
			if p.Cwd != wd || p.Start.IsZero() || p.Exe == "" {
				t.Errorf("self = %+v, want cwd %q", p, wd)
			}
			return
		}
	}
	t.Errorf("self (pid %d, %q) not listed in %d processes", os.Getpid(), self.Name, len(procs))
}
