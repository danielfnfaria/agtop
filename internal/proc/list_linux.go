//go:build linux

package proc

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func list(name string) ([]Process, error) {
	return listProc("/proc", name)
}

// listProc enumerates root/<pid> (a /proc tree) for the processes named name: comm, exe and cwd
// (symlinks), and the start time from stat with the boot time of root/stat.
func listProc(root, name string) ([]Process, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []Process
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 || !e.IsDir() {
			continue
		}
		base := filepath.Join(root, e.Name())
		comm, err := osReadFile(base + "/comm")
		if err != nil {
			continue
		}
		comm = strings.TrimSpace(comm)
		exe, _ := os.Readlink(base + "/exe")
		if !MatchesExecutable(name, comm, exe) {
			continue
		}
		p := Process{PID: pid, Name: comm, Exe: exe}
		p.Cwd, _ = os.Readlink(base + "/cwd")
		if text, err := osReadFile(base + "/stat"); err == nil {
			if st, err := parseProcStat(text); err == nil {
				p.Start = startTimeIn(root, st.startTicks)
			}
		}
		out = append(out, p)
	}
	return out, nil
}
