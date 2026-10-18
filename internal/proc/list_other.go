//go:build !darwin && !linux

package proc

// list is not supported outside Darwin and Linux: no process is reported.
func list(string) ([]Process, error) { return nil, errUnsupported }
