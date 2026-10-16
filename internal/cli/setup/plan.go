// Package setup implements `agtop setup`: wiring `statusline-hook` into the statusLine of each
// profile's settings.json (and, with --remove, undoing it). This file is the pure part: how the
// status line command is wrapped with the hook and unwrapped again, and how settings.json
// changes. No I/O.
package setup

import (
	"encoding/json"
	"regexp"
	"strings"
)

const (
	// hookMarker marks a status line command as already wired to the hook.
	hookMarker = "statusline-hook"
	// printFlag is the flag of statusline-hook that prints a short status line instead of
	// passing stdin through.
	printFlag = "--print"
	// savedVersion is the version of <configDir>/agtop/setup.json.
	savedVersion json.Number = "1"
)

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// ShellQuote is s as one POSIX shell word: unchanged when safe, else single-quoted.
func ShellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return `'` + strings.ReplaceAll(s, `'`, `'\''`) + `'`
}

// HookCommand is the shell command that runs the hook: plain `agtop statusline-hook` when
// onPath, else the absolute path of the executable.
func HookCommand(onPath bool, exe string) string {
	if onPath {
		return "agtop " + hookMarker
	}
	return ShellQuote(exe) + " " + hookMarker
}

const (
	wrapHead = `input=$(cat); printf '%s' "$input" | `
	wrapMid  = ` >/dev/null 2>&1; printf '%s' "$input" | `
)

// needsNewline: original needs a newline before the closing brace: a trailing ; or & would make
// `; }` a syntax error, and a comment or a multi-line command would swallow it.
func needsNewline(original string) bool {
	return strings.HasSuffix(original, ";") || strings.HasSuffix(original, "&") ||
		strings.Contains(original, "#") || strings.Contains(original, "\n")
}

// WrapCommand is the status line command with the hook in front: the hook gets a copy of stdin
// and its output is discarded; original gets stdin as before. The group `{ ...; }` makes the
// whole original command (with ; or &&) read the payload, not just its first part.
func WrapCommand(hook, original string) string {
	body := strings.TrimSpace(original)
	group := "{ " + body + "; }"
	if needsNewline(body) {
		group = "{ " + body + "\n}"
	}
	return wrapHead + hook + wrapMid + group
}

// PrintCommand is the command used when there is no status line to keep: the hook prints its
// own line.
func PrintCommand(hook string) string { return hook + " " + printFlag }

func balancedBraces(s string) bool {
	depth := 0
	for _, ch := range s {
		switch ch {
		case '{':
			depth++
		case '}':
			if depth--; depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

// Unwrapped is what a hooked command was made of: the print form, or a wrapped original.
type Unwrapped struct {
	Print    bool
	Original string
}

var (
	wrappedRE   = regexp.MustCompile(`^\s*input=\$\(cat\);\s*printf\s+'%s'\s+"\$input"\s*\|\s*(.+?)\s*>\s*/dev/null\s+2>&1\s*;\s*printf\s+'%s'\s+"\$input"\s*\|\s*([\s\S]+?)\s*$`)
	groupRE     = regexp.MustCompile(`^\{\s([\s\S]*?)(?:;\s*|\n)\}$`)
	printOnlyRE = regexp.MustCompile(`^[^;|&\n]*statusline-hook\s+--print\s*$`)
	pipedRE     = regexp.MustCompile(`^([^;|&\n]*statusline-hook)\s*\|\s*([\s\S]+?)\s*$`)
)

// UnwrapCommand undoes the known ways of wiring the hook: WrapCommand (and the same shape
// without the group, as written by hand), PrintCommand, and `<hook> | <original>`. It reports
// false for anything else.
func UnwrapCommand(command string) (Unwrapped, bool) {
	u, _, ok := unwrapHook(command)
	return u, ok
}

// unwrapHook is UnwrapCommand plus the part of the command that runs the hook.
func unwrapHook(command string) (u Unwrapped, hook string, ok bool) {
	if printOnlyRE.MatchString(command) {
		hook := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(command), printFlag))
		return Unwrapped{Print: true}, hook, true
	}
	if m := wrappedRE.FindStringSubmatch(command); m != nil && strings.Contains(m[1], hookMarker) {
		original := m[2]
		if g := groupRE.FindStringSubmatch(original); g != nil {
			if inner := strings.TrimSpace(g[1]); balancedBraces(inner) {
				original = inner
			}
		}
		if original == "" {
			return Unwrapped{}, "", false
		}
		return Unwrapped{Original: original}, strings.TrimSpace(m[1]), true
	}
	if m := pipedRE.FindStringSubmatch(command); m != nil {
		return Unwrapped{Original: m[2]}, strings.TrimSpace(m[1]), true
	}
	return Unwrapped{}, "", false
}

// IsHooked reports whether the status line command already runs the hook.
func IsHooked(statusLine any) bool {
	o, ok := statusLine.(*Object)
	if !ok {
		return false
	}
	cmd, ok := o.Get("command")
	s, isStr := cmd.(string)
	return ok && isStr && strings.Contains(s, hookMarker)
}

// Saved is what setup remembers, in <configDir>/agtop/setup.json, to undo itself.
type Saved struct {
	// Original is the statusLine before the setup; nil when there was none.
	Original *Object
	// Command is the command the setup installed.
	Command string
}

func (s Saved) toObject() *Object {
	o := NewObject()
	o.set("version", savedVersion)
	if s.Original == nil {
		o.set("original", nil)
	} else {
		o.set("original", s.Original)
	}
	o.set("command", s.Command)
	return o
}

// ParseSaved is the setup.json content, if it is valid.
func ParseSaved(value any) (Saved, bool) {
	o, ok := value.(*Object)
	if !ok {
		return Saved{}, false
	}
	if v, _ := o.Get("version"); v != savedVersion {
		return Saved{}, false
	}
	command, ok := o.vals["command"].(string)
	if !ok {
		return Saved{}, false
	}
	switch orig := o.vals["original"].(type) {
	case nil:
		if _, present := o.vals["original"]; !present {
			return Saved{}, false
		}
		return Saved{Command: command}, true
	case *Object:
		return Saved{Original: orig, Command: command}, true
	}
	return Saved{}, false
}

// Action is what a plan does.
type Action int

const (
	// ActionNone: nothing to do (remove: not hooked).
	ActionNone Action = iota
	// ActionAlready: setup is already in place.
	ActionAlready
	// ActionSkip: the profile cannot be changed; see Reason.
	ActionSkip
	// ActionApply: settings changes.
	ActionApply
)

// reasonUnknownForm is why a hooked status line in an unknown form is left alone.
const reasonUnknownForm = "statusLine runs statusline-hook in a form agtop does not know; edit it by hand"

// Plan is the outcome of PlanSetup or PlanRemove.
type Plan struct {
	Action Action
	Reason string
	// Before and After are the statusLine before and after (nil: none).
	Before, After *Object
	// Settings is the new settings object.
	Settings *Object
	// Saved is what setup must remember (PlanSetup only).
	Saved Saved
	// Updated: the status line already ran a hook, which now points to the given one
	// (PlanSetup only).
	Updated bool
}

// PlanSetup is what setup does to settings (the parsed settings.json) with the hook command.
// saved is the current setup.json, if any: when the status line is already hooked to another
// hook, the original it recorded is kept so --remove still restores the user's command.
func PlanSetup(settings *Object, hook string, saved *Saved) Plan {
	current, has := settings.Get("statusLine")
	if !has {
		command := PrintCommand(hook)
		after := NewObject()
		after.set("type", "command")
		after.set("command", command)
		return Plan{Action: ActionApply, After: after, Settings: settings.with("statusLine", after, false), Saved: Saved{Command: command}}
	}
	cur, ok := current.(*Object)
	var cmd string
	if ok {
		cmd, ok = cur.vals["command"].(string)
	}
	if !ok || strings.TrimSpace(cmd) == "" {
		return Plan{Action: ActionSkip, Reason: "statusLine has no command; wire the hook by hand"}
	}
	if strings.Contains(cmd, hookMarker) {
		return planUpdate(settings, cur, cmd, hook, saved)
	}
	command := WrapCommand(hook, cmd)
	after := cur.with("command", command, false)
	return Plan{Action: ActionApply, Before: cur, After: after, Settings: settings.with("statusLine", after, false), Saved: Saved{Original: cur, Command: command}}
}

// planUpdate handles a status line that already contains the hook marker: unchanged when it
// runs the wanted hook, rebuilt around the same original when it runs another one.
func planUpdate(settings, cur *Object, cmd, hook string, saved *Saved) Plan {
	un, current, ok := unwrapHook(cmd)
	if !ok {
		return Plan{Action: ActionSkip, Reason: reasonUnknownForm}
	}
	if current == hook {
		return Plan{Action: ActionAlready}
	}
	var command string
	var keep Saved
	if un.Print {
		command = PrintCommand(hook)
	} else {
		command = WrapCommand(hook, un.Original)
	}
	switch {
	case saved != nil && saved.Command == cmd:
		keep = Saved{Original: saved.Original, Command: command}
	case un.Print:
		keep = Saved{Command: command}
	default:
		keep = Saved{Original: cur.with("command", un.Original, false), Command: command}
	}
	after := cur.with("command", command, false)
	return Plan{Action: ActionApply, Before: cur, After: after, Settings: settings.with("statusLine", after, false), Saved: keep, Updated: true}
}

// PlanRemove is what setup --remove does to settings. With saved matching the installed
// command, the original command comes back from it; otherwise the known formats are unwrapped.
// The other fields of statusLine stay as they are now.
func PlanRemove(settings *Object, saved *Saved) Plan {
	current, _ := settings.Get("statusLine")
	if !IsHooked(current) {
		return Plan{Action: ActionNone}
	}
	cur := current.(*Object)
	command := cur.vals["command"].(string)
	var restored Unwrapped
	if saved != nil && saved.Command == command {
		if orig, ok := savedOriginalCommand(saved); ok {
			restored = Unwrapped{Original: orig}
		} else {
			restored = Unwrapped{Print: true}
		}
	} else {
		var ok bool
		if restored, ok = UnwrapCommand(command); !ok {
			return Plan{Action: ActionSkip, Reason: reasonUnknownForm}
		}
	}
	if restored.Print {
		return Plan{Action: ActionApply, Before: cur, Settings: settings.with("statusLine", nil, true)}
	}
	after := cur.with("command", restored.Original, false)
	return Plan{Action: ActionApply, Before: cur, After: after, Settings: settings.with("statusLine", after, false)}
}

func savedOriginalCommand(s *Saved) (string, bool) {
	if s.Original == nil {
		return "", false
	}
	c, ok := s.Original.vals["command"].(string)
	return c, ok
}

// DiffLines is a readable before/after of statusLine, one string per line.
func DiffLines(before, after *Object) []string {
	side := func(sign string, o *Object) []string {
		if o == nil {
			return []string{sign + " (no statusLine)"}
		}
		lines := strings.Split(`"statusLine": `+marshalJSON(o, "  "), "\n")
		for i, l := range lines {
			lines[i] = sign + " " + l
		}
		return lines
	}
	return append(side("-", before), side("+", after)...)
}
