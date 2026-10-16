//go:build !darwin && !linux

package proc

import "errors"

// errUnsupported is returned by Lookup on platforms agtop does not read process info from.
var errUnsupported = errors.New("process lookup is not supported on this platform")

// Lookup is not supported outside Darwin and Linux.
func Lookup(pid int) (Info, error) {
	return Info{}, errUnsupported
}
