// Package fdcount counts the descriptors this process has open (tests).
package fdcount

import (
	"os"
	"runtime"
)

// Open is the number of open descriptors, from /dev/fd on macOS and /proc/self/fd on Linux.
func Open() (int, error) {
	dir := "/proc/self/fd"
	if runtime.GOOS == "darwin" {
		dir = "/dev/fd"
	}
	f, err := os.Open(dir)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return 0, err
	}
	return len(names) - 1, nil // the descriptor of the listing itself
}
