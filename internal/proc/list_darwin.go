//go:build darwin

package proc

import (
	"bytes"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// proc_info(2) with PROC_INFO_CALL_PIDINFO and the PROC_PIDVNODEPATHINFO flavor returns a
// proc_vnodepathinfo: two vnode_info_path (cwd, then root), each a vnode_info (152 bytes) followed
// by a MAXPATHLEN path. The raw syscall needs no cgo.
const (
	procInfoCallPidInfo  = 2
	procPidVnodePathInfo = 9
	vnodeInfoSize        = 152
	maxPathLen           = 1024
	vnodeInfoPathSize    = vnodeInfoSize + maxPathLen
	vnodePathInfoSize    = 2 * vnodeInfoPathSize
)

func list(name string) ([]Process, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	var out []Process
	for _, kp := range procs {
		comm := cString(kp.Proc.P_comm[:])
		pid := int(kp.Proc.P_pid)
		var exe string
		if raw, err := unix.SysctlRaw("kern.procargs2", pid); err == nil {
			exe = exeFromProcArgs2(raw)
		}
		if comm != name && (exe == "" || filepath.Base(exe) != name) {
			continue
		}
		out = append(out, Process{
			PID:   pid,
			Name:  comm,
			Exe:   exe,
			Cwd:   cwdOf(pid),
			Start: time.Unix(kp.Proc.P_starttime.Sec, int64(kp.Proc.P_starttime.Usec)*1000).UTC(),
		})
	}
	return out, nil
}

// exeFromProcArgs2 is the executable path at the head of a kern.procargs2 buffer (after argc).
func exeFromProcArgs2(b []byte) string {
	if len(b) <= 4 {
		return ""
	}
	return cString(b[4:])
}

// cwdOf reads the cwd of pid through proc_info; "" when not permitted or unknown.
func cwdOf(pid int) string {
	buf := make([]byte, vnodePathInfoSize)
	n, _, errno := syscall.Syscall6(syscall.SYS_PROC_INFO, procInfoCallPidInfo, uintptr(pid), procPidVnodePathInfo, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if errno != 0 || int(n) < vnodePathInfoSize {
		return ""
	}
	return parseVnodePathInfo(buf)
}

// parseVnodePathInfo is the cwd path of a proc_vnodepathinfo buffer.
func parseVnodePathInfo(buf []byte) string {
	if len(buf) < vnodePathInfoSize {
		return ""
	}
	path := buf[vnodeInfoSize:vnodeInfoPathSize]
	if i := bytes.IndexByte(path, 0); i >= 0 {
		path = path[:i]
	}
	return string(path)
}
