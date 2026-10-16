package discovery

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOSFSReadFile(t *testing.T) {
	root := makeTmp(t)
	tests := []struct {
		name, content string
		tooLarge      bool
	}{
		{"small", "abc", false},
		{"empty", "", false},
		{"exact", strings.Repeat("x", 16), false},
		{"big", strings.Repeat("x", 17), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(root, tt.name)
			writeFile(t, file, tt.content)
			got, err := OSFS{}.ReadFile(file, 16)
			if tt.tooLarge {
				if !errors.Is(err, ErrFileTooLarge) {
					t.Fatalf("err = %v, want ErrFileTooLarge", err)
				}
				return
			}
			if err != nil || got != tt.content {
				t.Errorf("got %q, %v", got, err)
			}
		})
	}
}

func TestReadCappedGrowthAfterStat(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		tooLarge bool
	}{
		{"grown but fits", "0123456789", false},
		{"grown past the cap", strings.Repeat("x", 40), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readCapped(strings.NewReader(tt.data), 16)
			if tt.tooLarge {
				if !errors.Is(err, ErrFileTooLarge) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil || got != tt.data {
				t.Errorf("got %q, %v", got, err)
			}
		})
	}
}

func TestFileTooLargeErrorHasNoContent(t *testing.T) {
	file := filepath.Join(makeTmp(t), "f")
	writeFile(t, file, "SECRET-"+strings.Repeat("x", 40))
	_, err := OSFS{}.ReadFile(file, 8)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("err = %v", err)
	}
}

func TestStatSignature(t *testing.T) {
	file := filepath.Join(makeTmp(t), "agent-a1.meta.json")
	writeFile(t, file, "{}")
	set := func(sec int64) {
		ts := time.Unix(sec, 0)
		if err := os.Chtimes(file, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	set(1_000)
	if sig, ok := StatSignature(OSFS{}, file); !ok || sig != "2:1000000" {
		t.Errorf("got %q, %v", sig, ok)
	}
	writeFile(t, file, `{"a":1}`)
	set(2_000)
	if sig, ok := StatSignature(OSFS{}, file); !ok || sig != "7:2000000" {
		t.Errorf("got %q, %v", sig, ok)
	}
	if _, ok := StatSignature(OSFS{}, file+".nope"); ok {
		t.Error("want no signature for a missing file")
	}
}

type noSigFS struct{ OSFS }

func (noSigFS) Stat(string) (Info, error) { return Info{IsFile: true}, nil }

func TestStatSignatureNotReported(t *testing.T) {
	if _, ok := StatSignature(noSigFS{}, "/x/f"); ok {
		t.Error("want no signature when the fs reports neither size nor mtime")
	}
}
