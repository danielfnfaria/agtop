package hook

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"

	"github.com/danielfnfaria/agtop/internal/domain"
)

// writeAtomic writes content to <dir>/<name> (file 0600) atomically: a temporary file in the
// same directory, then rename.
func writeAtomic(dir, name string, content []byte) (err error) {
	tmp := filepath.Join(dir, fmt.Sprintf(".%s.%d.%s.tmp", name, os.Getpid(), strconv.FormatUint(rand.Uint64(), 36)))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(content); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}

// readSmall reads a regular file of at most max bytes; anything wrong means "none".
func readSmall(file string, max int64) ([]byte, bool) {
	st, err := os.Stat(file)
	if err != nil || !st.Mode().IsRegular() || st.Size() > max {
		return nil, false
	}
	data, err := os.ReadFile(file)
	return data, err == nil
}

func ensureDir(configDir string) (string, error) {
	dir := filepath.Join(configDir, domain.UsageDirName)
	return dir, os.MkdirAll(dir, 0o700)
}

// RecordUsage records limits in <configDir>/agtop/usage.json (dir 0700, file 0600),
// atomically. It skips the write when the content is the same and the file is younger than
// RewriteAfterMs.
func RecordUsage(configDir string, limits domain.RateLimits, nowMs int64) (bool, error) {
	dir, err := ensureDir(configDir)
	if err != nil {
		return false, err
	}
	if data, ok := readSmall(filepath.Join(dir, domain.UsageFileName), domain.MaxUsageFileBytes); ok {
		if cur, ok := domain.UnmarshalUsageFile(data); ok {
			age := nowMs - cur.UpdatedAt
			same := domain.SameRateLimits(domain.RateLimits{FiveHour: cur.FiveHour, SevenDay: cur.SevenDay}, limits)
			if same && age >= 0 && age <= RewriteAfterMs {
				return false, nil
			}
		}
	}
	content, err := domain.MarshalUsageFile(domain.UsageFile{
		Version: domain.UsageFileVersion, UpdatedAt: nowMs, FiveHour: limits.FiveHour, SevenDay: limits.SevenDay,
	})
	if err != nil {
		return false, err
	}
	return true, writeAtomic(dir, domain.UsageFileName, append(content, '\n'))
}

// RecordContextWindow records the context window of one session in
// <configDir>/agtop/context.json (same directory, permissions and atomic write as the usage
// file). It skips the write when the session's entry is unchanged and recent. Concurrent hooks
// may lose an entry; the next status line refresh of that session puts it back.
func RecordContextWindow(configDir string, report domain.ContextReport, nowMs int64) (bool, error) {
	dir, err := ensureDir(configDir)
	if err != nil {
		return false, err
	}
	var current *domain.ContextFile
	if data, ok := readSmall(filepath.Join(dir, domain.ContextFileName), domain.MaxContextFileBytes); ok {
		if f, ok := domain.UnmarshalContextFile(data); ok {
			current = &f
		}
	}
	next, write := domain.UpsertContextEntry(current, report, nowMs)
	if !write {
		return false, nil
	}
	content, err := json.Marshal(next)
	if err != nil {
		return false, err
	}
	return true, writeAtomic(dir, domain.ContextFileName, append(content, '\n'))
}
