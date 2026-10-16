package discovery

import (
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/domain"
)

// MaxRegistryFileBytes caps a sessions/<pid>.json.
const MaxRegistryFileBytes = 64 * 1024

const (
	maxCwdLength   = 500
	maxShortLength = 64
	// An unknown registry status is shown as is, cut to this length.
	maxStatusLength = 16
	// Slack against clock/uptime imprecision when comparing startedAt with the boot.
	bootClockSlackMs = 5_000
	// Largest integer a JavaScript number holds exactly; epoch values beyond it are not real.
	maxSafeEpoch = 1 << 53
)

// registryFileRE accepts only <positive integer>.json; <pid>.<hash>.key and the like are not.
var registryFileRE = regexp.MustCompile(`^([1-9][0-9]{0,9})\.json$`)

// ReadSessionRegistry reads <dir>/sessions/*.json and returns the valid records with the
// liveness of the pid. A record whose startedAt precedes the boot (minus the slack) is dead
// without asking the pid, which may have been reused; without startedAt only the pid counts.
// Invalid files are skipped and counted in the diagnostics (never with file content).
func ReadSessionRegistry(profile config.ProfileDir, deps Deps) RegistryResult {
	diag := Diagnostics{Issues: []RegistryIssue{}}
	sessionsDir := filepath.Join(profile.Dir, "sessions")
	names, err := deps.FS.ReadDir(sessionsDir)
	if err != nil {
		diag.SessionsDirMissing = true
		return RegistryResult{Diagnostics: diag}
	}
	slices.Sort(names)

	bootAt := deps.Now() - deps.UptimeMs()
	var records []LiveSessionRecord
	for _, file := range names {
		m := registryFileRE.FindStringSubmatch(file)
		if m == nil {
			diag.SkippedEntries++
			continue
		}
		filePID, _ := strconv.Atoi(m[1])

		text, err := deps.FS.ReadFile(filepath.Join(sessionsDir, file), MaxRegistryFileBytes)
		if err != nil {
			reason := IssueReadError
			if errors.Is(err, ErrFileTooLarge) {
				reason = IssueTooLarge
			}
			diag.Issues = append(diag.Issues, RegistryIssue{File: file, Reason: reason})
			continue
		}
		record, reason := parseRegistryEntry(text, profile, filePID)
		if reason != "" {
			diag.Issues = append(diag.Issues, RegistryIssue{File: file, Reason: reason})
			continue
		}
		records = append(records, withLiveness(record, bootAt, deps))
	}
	return RegistryResult{Records: records, Diagnostics: diag}
}

func withLiveness(r LiveSessionRecord, bootAt int64, deps Deps) LiveSessionRecord {
	if r.StartedAt != 0 && r.StartedAt < bootAt-bootClockSlackMs {
		r.DeadReason = DeadPreBoot
		return r
	}
	if deps.IsPidAlive(r.PID) {
		r.Alive = true
	} else {
		r.DeadReason = DeadPid
	}
	return r
}

func parseRegistryEntry(text string, profile config.ProfileDir, filePID int) (LiveSessionRecord, IssueReason) {
	var raw any
	if json.Unmarshal([]byte(text), &raw) != nil {
		return LiveSessionRecord{}, IssueInvalidJSON
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return LiveSessionRecord{}, IssueNotObject
	}
	pidVal, hasPID := obj["pid"]
	sidVal, hasSID := obj["sessionId"]
	if !hasPID || !hasSID {
		return LiveSessionRecord{}, IssueMissingFields
	}
	pidF, ok := pidVal.(float64)
	if !ok || pidF != math.Trunc(pidF) || !IsValidPID(int(pidF)) || pidF > maxPID {
		return LiveSessionRecord{}, IssueInvalidPID
	}
	pid := int(pidF)
	if pid != filePID {
		return LiveSessionRecord{}, IssuePIDMismatch
	}
	sid, ok := sidVal.(string)
	if !ok || !IsSafeID(sid) {
		return LiveSessionRecord{}, IssueInvalidSessionID
	}

	r := LiveSessionRecord{Profile: profile, PID: pid, SessionID: sid, Activity: toActivity(obj["status"])}
	if r.Activity == ActivityUnknown {
		r.RawStatus = displayString(obj["status"], maxStatusLength)
	}
	r.Cwd = displayString(obj["cwd"], maxCwdLength)
	r.Kind = displayString(obj["kind"], maxShortLength)
	r.Version = displayString(obj["version"], maxShortLength)
	r.Name = displayString(obj["name"], domain.DefaultMaxLength)
	r.UpdatedAt = epochMs(obj["updatedAt"])
	r.StartedAt = epochMs(obj["startedAt"])
	return r, ""
}

func toActivity(status any) Activity {
	s, _ := status.(string)
	switch a := Activity(s); a {
	case ActivityBusy, ActivityShell, ActivityWaiting, ActivityIdle:
		return a
	}
	return ActivityUnknown
}

func displayString(v any, maxLength int) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return domain.Sanitize(s, maxLength)
}

func epochMs(v any) int64 {
	f, ok := v.(float64)
	if !ok || f <= 0 || f > maxSafeEpoch {
		return 0
	}
	return int64(f)
}
