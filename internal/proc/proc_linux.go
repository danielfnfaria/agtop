package proc

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// errStat is the error of a malformed /proc/<pid>/stat line.
var errStat = errors.New("malformed /proc/<pid>/stat")

func osReadFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

func notFound(err error) error {
	if os.IsNotExist(err) {
		return ErrNotFound
	}
	return err
}

// clockTicksPerSecond is USER_HZ, the rate of the starttime ticks in /proc/<pid>/stat. It is
// 100 on the architectures agtop builds for (amd64, arm64).
const clockTicksPerSecond = 100

// Lookup reads /proc/<pid> for the process info.
func Lookup(pid int) (Info, error) {
	base := "/proc/" + strconv.Itoa(pid)
	text, err := osReadFile(base + "/stat")
	if err != nil {
		return Info{}, notFound(err)
	}
	st, err := parseProcStat(text)
	if err != nil {
		return Info{}, err
	}
	status, err := osReadFile(base + "/status")
	if err != nil {
		return Info{}, notFound(err)
	}
	owner, err := parseProcStatusUID(status)
	if err != nil {
		return Info{}, err
	}
	info := Info{PID: st.pid, PPID: st.ppid, Name: st.comm, Owner: owner}
	if b, err := osReadFile(base + "/cmdline"); err == nil {
		info.Args = parseProcCmdline(b)
	}
	info.Start = startTime(st.startTicks)
	return info, nil
}

type procStat struct {
	pid        int
	comm       string
	ppid       int
	startTicks uint64
}

// parseProcStat parses the single line of /proc/<pid>/stat. The comm field may contain spaces
// and parentheses, so it is read between the first '(' and the last ')'.
func parseProcStat(line string) (procStat, error) {
	open := strings.IndexByte(line, '(')
	close := strings.LastIndexByte(line, ')')
	if open <= 0 || close < open {
		return procStat{}, errStat
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line[:open]))
	if err != nil {
		return procStat{}, errStat
	}
	comm := line[open+1 : close]
	rest := strings.Fields(line[close+2:])
	// rest[0] is the state (field 3): ppid (field 4) is rest[1] and starttime (field 22) is
	// rest[19].
	if len(rest) < 20 {
		return procStat{}, errStat
	}
	ppid, err := strconv.Atoi(rest[1])
	if err != nil {
		return procStat{}, errStat
	}
	ticks, err := strconv.ParseUint(rest[19], 10, 64)
	if err != nil {
		return procStat{}, errStat
	}
	return procStat{pid: pid, comm: comm, ppid: ppid, startTicks: ticks}, nil
}

// parseProcStatusUID reads the real uid (the first of "Uid:\t...") from /proc/<pid>/status. It
// fails on a missing line, an empty field or a non-integer, so an unknown owner is never mistaken
// for root (uid 0) and a malformed status is surfaced instead of silently trusted.
func parseProcStatusUID(text string) (int, error) {
	for _, line := range strings.Split(text, "\n") {
		rest, ok := strings.CutPrefix(line, "Uid:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return 0, errors.New("status Uid line is empty")
		}
		uid, err := strconv.Atoi(fields[0])
		if err != nil {
			return 0, fmt.Errorf("invalid status uid %q: %w", fields[0], err)
		}
		return uid, nil
	}
	return 0, errors.New("status has no Uid line")
}

// parseProcCmdline splits the NUL-separated argv of /proc/<pid>/cmdline.
func parseProcCmdline(b string) []string {
	var args []string
	for _, part := range strings.Split(b, "\x00") {
		if part == "" {
			continue
		}
		args = append(args, part)
	}
	return args
}

// startTime is the process start time in UTC from the boot-relative starttime ticks. A zero
// time means the boot time was unreadable.
func startTime(startTicks uint64) time.Time {
	b, err := osReadFile("/proc/stat")
	if err != nil {
		return time.Time{}
	}
	for _, line := range strings.Split(b, "\n") {
		rest, ok := strings.CutPrefix(line, "btime ")
		if !ok {
			continue
		}
		secs, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
		if err != nil {
			return time.Time{}
		}
		return ticksToTime(secs, startTicks)
	}
	return time.Time{}
}

// ticksToTime turns a boot time and the boot-relative starttime ticks into a wall clock, keeping
// the sub-second precision of the ticks instead of truncating to seconds.
func ticksToTime(bootSecs int64, startTicks uint64) time.Time {
	nsPerTick := int64(time.Second) / clockTicksPerSecond
	return time.Unix(bootSecs+int64(startTicks)/clockTicksPerSecond,
		int64(startTicks%clockTicksPerSecond)*nsPerTick).UTC()
}
