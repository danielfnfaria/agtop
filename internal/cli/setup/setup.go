package setup

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/errno"
)

const (
	// SetupFileName is the file, inside <configDir>/agtop, with what setup replaced.
	SetupFileName = "setup.json"
	// SettingsFileName is the settings file setup edits, inside the config directory.
	SettingsFileName = "settings.json"
	// BackupInfix is the infix of the backups of settings.json.
	BackupInfix = ".bak-agtop-"
)

// Options are the flags of `agtop setup`.
type Options struct {
	// Remove undoes the setup instead.
	Remove bool
	// Yes applies without asking.
	Yes bool
	// DryRun only shows what would change.
	DryRun bool
}

// Hook is how to run the hook and, if any, a warning about it.
type Hook struct {
	Command string
	Warning string
}

// IO is what setup talks to.
type IO struct {
	Stdout, Stderr io.Writer
	// IsTTY: stdin is a terminal (the confirmation can be asked).
	IsTTY bool
	// Confirm asks a yes/no question; true for yes.
	Confirm func(question string) bool
	// Cwd is the project directory (its .claude/settings*.json can override the status line).
	Cwd  string
	Now  func() time.Time
	Hook Hook
}

type status string

const (
	statusSetUp     status = "set up"
	statusAlready   status = "already set up"
	statusUpdated   status = "updated"
	statusRemoved   status = "removed"
	statusNotSetUp  status = "not set up"
	statusSkipped   status = "skipped"
	statusError     status = "error"
	statusWouldSet  status = "would set up"
	statusWouldUpd  status = "would update"
	statusWouldUndo status = "would remove"
)

// work is what will be done to one profile.
type work struct {
	profile      config.ProfileDir
	settingsFile string
	// originalText is the settings.json text as read; exists is false if it did not exist.
	originalText string
	exists       bool
	// newText is the new settings.json text; nil when it is not touched.
	newText *string
	// updated: the profile was set up with another hook (setup).
	updated bool
	// saved is what to write in setup.json (setup).
	saved *Saved
	// cleanup is the files of <configDir>/agtop to delete (remove).
	cleanup []string
	diff    []string
	status  status
	note    string
	// warnings were found while reading (shown before the confirmation); lateWarnings while
	// writing (shown with the summary).
	warnings     []string
	lateWarnings []string
}

func (w *work) pending() bool { return w.newText != nil || len(w.cleanup) > 0 }

func agtopDir(dir string) string { return filepath.Join(dir, domain.UsageDirName) }

func readText(file string) (text string, exists bool, err error) {
	b, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	return string(b), err == nil, err
}

func isDirectory(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func realpathOr(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

// overridingFiles are the settings files that, defining statusLine, win over <dir>/settings.json.
func overridingFiles(dir, cwd string) []string {
	own := realpathOr(filepath.Join(dir, SettingsFileName))
	candidates := []string{
		filepath.Join(dir, "settings.local.json"),
		filepath.Join(cwd, ".claude", "settings.json"),
		filepath.Join(cwd, ".claude", "settings.local.json"),
	}
	var out []string
	for _, file := range candidates {
		if realpathOr(file) == own {
			continue
		}
		text, ok, _ := readText(file)
		if !ok {
			continue
		}
		// Missing or unreadable files override nothing.
		if v, err := ParseJSON(text); err == nil {
			if o, isObj := v.(*Object); isObj {
				if _, has := o.Get("statusLine"); has {
					out = append(out, file)
				}
			}
		}
	}
	return out
}

func readSaved(dir string) *Saved {
	text, ok, _ := readText(filepath.Join(agtopDir(dir), SetupFileName))
	if !ok {
		return nil
	}
	v, err := ParseJSON(text)
	if err != nil {
		return nil
	}
	s, ok := ParseSaved(v)
	if !ok {
		return nil
	}
	return &s
}

// ownedFiles are the files agtop owns in <configDir>/agtop (including leftovers of interrupted
// writes).
func ownedFiles(dir string) []string {
	entries, err := os.ReadDir(agtopDir(dir))
	if err != nil {
		return nil
	}
	own := []string{domain.UsageFileName, domain.ContextFileName, SetupFileName}
	ours := func(n string) bool {
		for _, o := range own {
			if n == o || (strings.HasSuffix(n, ".tmp") && strings.HasPrefix(n, "."+o+".")) {
				return true
			}
		}
		return false
	}
	var out []string
	for _, e := range entries {
		if ours(e.Name()) {
			out = append(out, filepath.Join(agtopDir(dir), e.Name()))
		}
	}
	return out
}

func errText(err error) string {
	if c := errno.Code(err); c != "" {
		return c
	}
	return err.Error()
}

// inspect reads one profile and works out what to do (nothing is written here).
func inspect(profile config.ProfileDir, remove bool, io IO) *work {
	w := &work{profile: profile, settingsFile: filepath.Join(profile.Dir, SettingsFileName), status: statusSkipped}
	if profile.Provider != "" && profile.Provider != domain.ClaudeCode {
		w.note = domain.ProviderInfo(profile.Provider).DisplayName + " has no status line to wire; not a Claude Code profile"
		return w
	}
	if !isDirectory(profile.Dir) {
		w.note = profile.Dir + " is not a directory"
		return w
	}
	text, exists, err := readText(w.settingsFile)
	if err != nil {
		w.status = statusError
		w.note = fmt.Sprintf("cannot read %s: %s", w.settingsFile, errText(err))
		return w
	}
	w.originalText, w.exists = text, exists
	settings := NewObject()
	if exists {
		v, err := ParseJSON(text)
		if err != nil {
			w.status = statusError
			w.note = fmt.Sprintf("%s is not valid JSON (%s); left untouched", w.settingsFile, err)
			return w
		}
		o, ok := v.(*Object)
		if !ok {
			w.status = statusError
			w.note = w.settingsFile + " is not a JSON object; left untouched"
			return w
		}
		settings = o
	}
	var previous *string
	if exists {
		previous = &text
	}

	if remove {
		plan := PlanRemove(settings, readSaved(profile.Dir))
		cleanup := ownedFiles(profile.Dir)
		if plan.Action == ActionSkip {
			w.note = plan.Reason
			return w
		}
		if plan.Action == ActionApply {
			t := FormatJSON(plan.Settings, previous)
			w.newText = &t
			w.diff = DiffLines(plan.Before, plan.After)
		}
		if plan.Action == ActionNone && len(cleanup) == 0 {
			w.status = statusNotSetUp
			return w
		}
		w.cleanup = cleanup
		w.status = statusRemoved
		return w
	}

	plan := PlanSetup(settings, io.Hook.Command, readSaved(profile.Dir))
	switch plan.Action {
	case ActionAlready:
		w.status = statusAlready
	case ActionSkip:
		w.note = plan.Reason
	default:
		t := FormatJSON(plan.Settings, previous)
		w.newText = &t
		w.saved = &plan.Saved
		w.diff = DiffLines(plan.Before, plan.After)
		w.status = statusSetUp
		if plan.Updated {
			w.updated = true
			w.status = statusUpdated
		}
	}
	for _, file := range overridingFiles(profile.Dir, io.Cwd) {
		w.warnings = append(w.warnings, fmt.Sprintf("%s defines statusLine, which overrides %s", file, w.settingsFile))
	}
	return w
}

// stamp is a compact UTC timestamp for backup names: 20261015T120000Z.
func stamp(now time.Time) string { return now.UTC().Format("20060102T150405Z") }

// backup copies file next to itself as <file>.bak-agtop-<stamp> (same mode) and returns the copy.
func backup(file string, now time.Time) (string, error) {
	st, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	base := file + BackupInfix + stamp(now)
	for n := 1; ; n++ {
		target := base
		if n > 1 {
			target = base + "-" + strconv.Itoa(n)
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, st.Mode().Perm())
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.Write(data)
		cerr := f.Close()
		if err := errors.Join(werr, cerr, os.Chmod(target, st.Mode().Perm())); err != nil {
			return "", err
		}
		return target, nil
	}
}

// writeAtomic writes text to file atomically (temporary file in the same directory, then
// rename). A symlinked file is written through (its target is replaced, the link stays). It
// keeps the mode of an existing file; a new one gets newMode.
func writeAtomic(file, text string, newMode fs.FileMode) (err error) {
	target, mode := file, newMode
	if resolved, rerr := filepath.EvalSymlinks(file); rerr == nil {
		target = resolved
		st, serr := os.Stat(target)
		if serr != nil {
			return serr
		}
		mode = st.Mode().Perm()
	} else if !errors.Is(rerr, fs.ErrNotExist) {
		return rerr
	}
	tmp := filepath.Join(filepath.Dir(target), fmt.Sprintf(".%s.%d.%s.tmp", filepath.Base(target), os.Getpid(), strconv.FormatUint(rand.Uint64(), 36)))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	_, werr := f.WriteString(text)
	if err = errors.Join(werr, f.Close(), os.Chmod(tmp, mode)); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// apply applies one profile's change and returns the backup made, if any.
func apply(w *work, now time.Time) (string, error) {
	// Claude Code (or the user) may have changed the file since it was read and confirmed.
	text, exists, err := readText(w.settingsFile)
	if err != nil {
		return "", err
	}
	if text != w.originalText || exists != w.exists {
		return "", fmt.Errorf("%s changed since it was read; run again", w.settingsFile)
	}
	var backupFile string
	if w.newText != nil && w.exists {
		if backupFile, err = backup(w.settingsFile, now); err != nil {
			return "", err
		}
	}
	if w.saved != nil {
		dir := agtopDir(w.profile.Dir)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		if err := writeAtomic(filepath.Join(dir, SetupFileName), marshalJSON(w.saved.toObject(), "  ")+"\n", 0o600); err != nil {
			return "", err
		}
	}
	if w.newText != nil {
		if err := writeAtomic(w.settingsFile, *w.newText, 0o600); err != nil {
			return "", err
		}
	}
	if w.cleanup != nil {
		for _, file := range w.cleanup {
			if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return "", err
			}
		}
		err := os.Remove(agtopDir(w.profile.Dir))
		switch {
		case err == nil, errors.Is(err, fs.ErrNotExist):
		case errors.Is(err, syscall.ENOTEMPTY), errors.Is(err, syscall.EEXIST):
			w.lateWarnings = append(w.lateWarnings, agtopDir(w.profile.Dir)+" has other files; left in place")
		default:
			return "", err
		}
	}
	return backupFile, nil
}

// Run runs `agtop setup` (or --remove) over the profiles and returns the exit code.
func Run(profiles []config.ProfileDir, opts Options, io IO) int {
	out := func(s string) { fmt.Fprintln(io.Stdout, s) }
	errf := func(s string) { fmt.Fprintf(io.Stderr, "agtop: %s\n", s) }
	if !opts.Remove && io.Hook.Warning != "" {
		errf("warning: " + io.Hook.Warning)
	}

	works := make([]*work, len(profiles))
	for i, p := range profiles {
		works[i] = inspect(p, opts.Remove, io)
	}
	for _, w := range works {
		if !w.pending() {
			continue
		}
		out(fmt.Sprintf("[%s] %s", w.profile.Label, w.settingsFile))
		for _, line := range w.diff {
			out("  " + line)
		}
		for _, file := range w.cleanup {
			out("  delete " + file)
		}
		out("")
	}
	for _, w := range works {
		for _, warning := range w.warnings {
			errf(fmt.Sprintf("warning [%s]: %s", w.profile.Label, warning))
		}
	}

	var todo []*work
	for _, w := range works {
		if w.pending() {
			todo = append(todo, w)
		}
	}
	declined := false
	if len(todo) > 0 {
		switch {
		case opts.DryRun:
			for _, w := range todo {
				w.status = statusWouldSet
				if w.updated {
					w.status = statusWouldUpd
				}
				if opts.Remove {
					w.status = statusWouldUndo
				}
			}
		case !opts.Yes && !io.IsTTY:
			errf("stdin is not a terminal, so nothing was written; run again with --yes to apply")
			declined = true
		case !opts.Yes:
			verb := "Set up"
			if opts.Remove {
				verb = "Remove the hook from"
			}
			declined = !io.Confirm(fmt.Sprintf("%s %d profile(s)? [y/N] ", verb, len(todo)))
			if declined {
				out("Nothing written.")
			}
		}
	}

	failed := false
	for _, w := range works {
		failed = failed || w.status == statusError
	}
	changed := false
	if !opts.DryRun && !declined {
		for _, w := range todo {
			backupFile, err := apply(w, io.Now())
			if err != nil {
				w.status = statusError
				w.note = err.Error()
				failed = true
				continue
			}
			if backupFile != "" {
				w.note = "backup: " + backupFile
			}
			changed = true
		}
	}

	for _, w := range works {
		if declined && w.pending() {
			continue
		}
		note := ""
		if w.note != "" {
			note = " (" + w.note + ")"
		}
		out(fmt.Sprintf("[%s] %s%s", w.profile.Label, w.status, note))
		for _, warning := range w.lateWarnings {
			errf(fmt.Sprintf("warning [%s]: %s", w.profile.Label, warning))
		}
	}
	if changed {
		out("Claude Code picks up the new status line the next time it renders it.")
	}
	if declined && !io.IsTTY && !opts.Yes {
		return 1
	}
	if failed {
		return 1
	}
	return 0
}

var yesRE = regexp.MustCompile(`(?i)^\s*y(es)?\s*$`)

// IsYes reports whether answer is a yes.
func IsYes(answer string) bool { return yesRE.MatchString(answer) }

// ResolveHook is the command that runs the hook from this install: `agtop statusline-hook` when
// the first agtop on PATH is the running executable (same real path), else the executable's
// absolute path, shell-quoted. Nothing from settings.json is ever executed.
func ResolveHook(exe, pathEnv string) Hook {
	exeReal := realpathOr(exe)
	onPath := false
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, "agtop")
		st, err := os.Stat(candidate)
		if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0o111 == 0 {
			continue
		}
		// The first agtop on the PATH is the one the shell runs.
		onPath = realpathOr(candidate) == exeReal
		break
	}
	return Hook{Command: HookCommand(onPath, exeReal)}
}
