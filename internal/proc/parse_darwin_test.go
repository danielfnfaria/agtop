//go:build darwin

package proc

import (
	"encoding/binary"
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
