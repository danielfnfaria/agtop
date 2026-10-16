// Package errno names the system error behind an error, so that reports carry a short code and
// never the error text, which may quote a path or file content.
package errno

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"
)

// Code is the symbolic name of the errno in err's chain (ENOENT, EACCES...), or "" if there is
// none.
func Code(err error) string {
	var e syscall.Errno
	if !errors.As(err, &e) {
		return ""
	}
	return unix.ErrnoName(e)
}
