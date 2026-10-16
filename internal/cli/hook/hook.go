// Package hook implements `agtop statusline-hook`: it sits in front of the user's status line
// command, copies stdin to stdout unchanged and records the plan usage limits and the context
// window sizes found in it (only numbers and ids of the window) under <configDir>/agtop/. It
// always exits 0 and never writes anything else to stdout: a failure here must not break the
// user's status line.
//
// It runs on every status line refresh, so it stays light: no UI, config or store packages.
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"

	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/errno"
)

const (
	// MaxPayloadBytes: larger payloads are passed through but not parsed.
	MaxPayloadBytes = 1024 * 1024
	// RewriteAfterMs: an unchanged usage file is still rewritten after this long (keeps
	// updatedAt fresh).
	RewriteAfterMs = 60_000
)

// IO is what the hook reads from and writes to.
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// ClaudeConfigDir is the env CLAUDE_CONFIG_DIR of the profile running the hook.
	ClaudeConfigDir string
	Homedir         string
	// Debug turns on short error codes on stderr (env AGTOP_DEBUG).
	Debug bool
	// NowMs is the current time in epoch milliseconds.
	NowMs func() int64
}

// ConfigDir is the Claude Code config directory of the running profile: CLAUDE_CONFIG_DIR when
// set (a leading ~ is expanded), else ~/.claude. The status line command runs inside Claude
// Code, with the profile's environment.
func ConfigDir(claudeConfigDir, homedir string) string {
	dir := strings.TrimSpace(claudeConfigDir)
	if dir == "" {
		return filepath.Join(homedir, ".claude")
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		dir = filepath.Join(homedir, dir[1:])
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return filepath.Clean(dir)
}

// FormatStatusLine is the line `statusline-hook --print` shows: `agtop · 5h 6% · 7d 30%` (only
// the windows present; just `agtop` without any).
func FormatStatusLine(limits domain.RateLimits) string {
	parts := []string{"agtop"}
	if limits.FiveHour != nil {
		parts = append(parts, fmt.Sprintf("5h %d%%", int(math.Round(limits.FiveHour.Percent))))
	}
	if limits.SevenDay != nil {
		parts = append(parts, fmt.Sprintf("7d %d%%", int(math.Round(limits.SevenDay.Percent))))
	}
	return strings.Join(parts, " · ")
}

// Run runs the hook and returns the exit code, always 0. With print, it prints FormatStatusLine
// instead of copying stdin to stdout (used when there is no other status line command to feed).
func Run(h IO, print bool) int {
	// debug reports a short message, with the errno name of err when it has one (never the
	// error text, which may quote a path).
	debug := func(what string, err error, fallback string) {
		if !h.Debug {
			return
		}
		msg := "agtop statusline-hook: " + what
		if err != nil {
			code := errno.Code(err)
			if code == "" {
				code = fallback
			}
			msg += ": " + code
		}
		_, _ = io.WriteString(h.Stderr, msg+"\n")
	}
	var limits domain.RateLimits
	defer func() {
		// Never let a bug break the user's status line.
		if r := recover(); r != nil {
			debug("panic", nil, "")
		}
	}()
	// A read error leaves what arrived so far.
	input, _ := io.ReadAll(h.Stdin)
	if !print {
		// Pass the bytes on first: the user's status line should not wait for the files.
		if _, err := h.Stdout.Write(input); err != nil {
			debug("stdout", err, "WRITE")
		}
	}
	payload := parsePayload(input, debug)
	configDir := ConfigDir(h.ClaudeConfigDir, h.Homedir)
	now := h.NowMs()
	var ok bool
	if limits, ok = domain.ExtractRateLimits(payload); ok {
		if _, err := RecordUsage(configDir, limits, now); err != nil {
			debug("failed", err, "error")
		}
	}
	// Independent files: a failure in one must not skip the other.
	if report, ok := domain.ExtractContextWindow(payload); ok {
		if _, err := RecordContextWindow(configDir, report, now); err != nil {
			debug("context failed", err, "error")
		}
	}
	if print {
		if _, err := io.WriteString(h.Stdout, FormatStatusLine(limits)+"\n"); err != nil {
			debug("stdout", err, "WRITE")
		}
	}
	return 0
}

func parsePayload(input []byte, debug func(string, error, string)) any {
	if len(input) == 0 || len(input) > MaxPayloadBytes {
		return nil
	}
	var payload any
	if err := json.Unmarshal(input, &payload); err != nil {
		debug("stdin is not JSON", nil, "")
		return nil
	}
	return payload
}
