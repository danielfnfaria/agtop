package errno

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCode(t *testing.T) {
	_, notExist := os.Open(filepath.Join(t.TempDir(), "nope"))
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"path error", notExist, "ENOENT"},
		{"wrapped", fmt.Errorf("x: %w", syscall.EACCES), "EACCES"},
		{"no errno", errors.New("SECRET"), ""},
		{"fs sentinel without errno", fs.ErrNotExist, ""},
		{"nil", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Code(tt.err); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
