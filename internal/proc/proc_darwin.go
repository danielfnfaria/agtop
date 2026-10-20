package proc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

// Lookup reads the process info through sysctl (kern.proc.pid and kern.procargs2).
func Lookup(pid int) (Info, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		// x/sys reports EIO when kern.proc.pid answers with no entry, i.e. the pid is gone.
		if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) || errors.Is(err, unix.EIO) {
			return Info{}, ErrNotFound
		}
		return Info{}, err
	}
	info := Info{
		PID:   int(kp.Proc.P_pid),
		PPID:  int(kp.Eproc.Ppid),
		Name:  cString(kp.Proc.P_comm[:]),
		Owner: int(kp.Eproc.Ucred.Uid),
		Start: time.Unix(kp.Proc.P_starttime.Sec, int64(kp.Proc.P_starttime.Usec)*1000).UTC(),
	}
	if raw, err := unix.SysctlRaw("kern.procargs2", pid); err == nil {
		info.Args = parseProcArgs2(raw)
	}
	return info, nil
}

// cString is a NUL-terminated fixed array as a string.
func cString(b []byte) string {
	before, _, _ := bytes.Cut(b, []byte{0})
	return string(before)
}

// parseProcArgs2 parses a kern.procargs2 buffer: an int32 argc, then the executable path
// (NUL-terminated), optional NUL padding, then the argc argv strings and the environment.
func parseProcArgs2(b []byte) []string {
	if len(b) < 4 {
		return nil
	}
	argc := int(binary.NativeEndian.Uint32(b[:4]))
	if argc <= 0 {
		return nil
	}
	rest := b[4:]
	// Skip the executable path and the NUL padding before argv.
	if i := bytes.IndexByte(rest, 0); i >= 0 {
		rest = rest[i:]
	}
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	args := make([]string, 0, min(argc, 64))
	for len(rest) > 0 && rest[0] != 0 && len(args) < argc {
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			args = append(args, string(rest))
			break
		}
		args = append(args, string(rest[:end]))
		rest = rest[end+1:]
	}
	return args
}
