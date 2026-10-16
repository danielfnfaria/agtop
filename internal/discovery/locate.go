package discovery

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/domain"
)

// MaxCustomTitleBytes caps custom-title.json.
const MaxCustomTitleBytes = 64 * 1024

// LocateMissTTLMs is how long a session without transcript does not trigger a new scan of
// projects/*.
const LocateMissTTLMs = 10_000

// scanBatch is how many projects are probed in parallel during a scan.
const scanBatch = 32

var (
	subagentFileRE = regexp.MustCompile(`^agent-(.+?)(\.meta\.json|\.jsonl)$`)
	nonAlnumRE     = regexp.MustCompile(`[^A-Za-z0-9]`)
	hasAlnumRE     = regexp.MustCompile(`[A-Za-z0-9]`)
)

func nowMs() int64 { return time.Now().UnixMilli() }

// ProjectDirHint is the likely projects/ folder name for a cwd: every character outside
// [A-Za-z0-9] becomes "-" (Claude Code's current rule). Only a hint: the scan stays the source
// of truth.
func ProjectDirHint(cwd string) (string, bool) {
	if cwd == "" {
		return "", false
	}
	name := nonAlnumRE.ReplaceAllString(cwd, "-")
	if !hasAlnumRE.MatchString(name) {
		return "", false
	}
	return name, true
}

// LocateCacheKey is the key of a session in the locate and inactive-files caches.
func LocateCacheKey(profileDir, sessionID string) string { return profileDir + "\x00" + sessionID }

// LocateOptions tune LocateSessionFiles.
type LocateOptions struct {
	// Cwd is the registry cwd, used as the project hint before the scan.
	Cwd string
}

// LocateSessionFiles finds the files of a session under <dir>/projects/*. Order: the cached
// project, the project guessed from the cwd, then a scan of projects/<any>/<sessionId>.jsonl,
// which does not depend on the cwd escaping rule. Without a transcript the scan is not
// repeated for LocateMissTTLMs. Every path returned is canonical and inside
// <dir>/projects.
func LocateSessionFiles(profile config.ProfileDir, sessionID string, deps Deps, opts LocateOptions) SessionFiles {
	if !IsSafeID(sessionID) {
		return SessionFiles{}
	}
	root, ok := NewContainmentRoot(deps.FS, filepath.Join(profile.Dir, "projects"))
	if !ok {
		return SessionFiles{}
	}
	found := findProject(deps, root, LocateCacheKey(profile.Dir, sessionID), sessionID, opts)

	files := SessionFiles{TranscriptPath: found.transcriptPath}
	if found.sessionDir == "" {
		return files
	}
	files.SessionDir = found.sessionDir
	files.SubagentsDir, _ = resolveKind(deps.FS, root, filepath.Join(found.sessionDir, "subagents"), kindDir)
	files.CustomTitle = readCustomTitle(deps.FS, root, found.sessionDir)
	return files
}

type projectProbe struct {
	transcriptPath string
	sessionDir     string
}

func findProject(deps Deps, root ContainmentRoot, cacheKey, sessionID string, opts LocateOptions) projectProbe {
	cached, hasCached := deps.LocateCache.Get(cacheKey)
	tried := map[string]bool{}
	// Best result so far: the project with the transcript, or the first with the folder.
	var best projectProbe

	tryProject := func(project string) bool {
		if project == "" || tried[project] {
			return false
		}
		tried[project] = true
		found := probeProject(deps.FS, root, project, sessionID)
		if found.transcriptPath != "" {
			deps.LocateCache.Set(cacheKey, LocateEntry{Kind: LocateFound, Project: project})
			best = found
			return true
		}
		if found.sessionDir != "" && best.sessionDir == "" {
			best = found
		}
		return false
	}

	if hasCached && tryProject(cached.Project) {
		return best
	}
	if hint, ok := ProjectDirHint(opts.Cwd); ok && tryProject(hint) {
		return best
	}

	now := deps.Now()
	if hasCached && cached.Kind == LocateMissing && now < cached.Until {
		return best
	}

	// No cache, expired, or the transcript vanished/moved: search again.
	project, found := searchProjects(deps.FS, root, sessionID)
	if found.transcriptPath != "" && project != "" {
		deps.LocateCache.Set(cacheKey, LocateEntry{Kind: LocateFound, Project: project})
		return found
	}
	deps.LocateCache.Set(cacheKey, LocateEntry{Kind: LocateMissing, Until: now + LocateMissTTLMs, Project: project})
	if found.sessionDir != "" {
		return found
	}
	return best
}

// searchProjects scans projects/* (in parallel batches, in name order): the first project with
// the transcript, else the first with the session folder.
func searchProjects(fs FS, root ContainmentRoot, sessionID string) (string, projectProbe) {
	names, err := fs.ReadDir(root.Dir)
	if err != nil {
		return "", projectProbe{}
	}
	slices.Sort(names)
	var fallbackProject string
	var fallback projectProbe
	for i := 0; i < len(names); i += scanBatch {
		batch := names[i:min(i+scanBatch, len(names))]
		probes := make([]projectProbe, len(batch))
		var wg sync.WaitGroup
		for j, name := range batch {
			wg.Add(1)
			go func() {
				defer wg.Done()
				probes[j] = probeProject(fs, root, name, sessionID)
			}()
		}
		wg.Wait()
		for j, found := range probes {
			if found.transcriptPath != "" {
				return batch[j], found
			}
			if found.sessionDir != "" && fallbackProject == "" {
				fallbackProject, fallback = batch[j], found
			}
		}
	}
	return fallbackProject, fallback
}

func probeProject(fs FS, root ContainmentRoot, project, sessionID string) projectProbe {
	projectDir := filepath.Join(root.Dir, project)
	var out projectProbe
	out.transcriptPath, _ = resolveKind(fs, root, filepath.Join(projectDir, sessionID+".jsonl"), kindFile)
	out.sessionDir, _ = resolveKind(fs, root, filepath.Join(projectDir, sessionID), kindDir)
	return out
}

type entryKind int

const (
	kindFile entryKind = iota
	kindDir
)

// resolveKind requires candidate to stay under root also after symlinks. As the candidate may
// already be built over a canonical path, either form of the root is accepted.
func resolveKind(fs FS, root ContainmentRoot, candidate string, kind entryKind) (string, bool) {
	lexicalRoot := root.Dir
	if strings.HasPrefix(candidate, root.Real+string(filepath.Separator)) {
		lexicalRoot = root.Real
	}
	real, ok := ResolveContained(fs, ContainmentRoot{Dir: lexicalRoot, Real: root.Real}, candidate)
	if !ok {
		return "", false
	}
	info, err := fs.Stat(real)
	if err != nil || (kind == kindFile && !info.IsFile) || (kind == kindDir && !info.IsDir) {
		return "", false
	}
	return real, true
}

func readCustomTitle(fs FS, root ContainmentRoot, sessionDir string) string {
	file, ok := resolveKind(fs, root, filepath.Join(sessionDir, "custom-title.json"), kindFile)
	if !ok {
		return ""
	}
	raw, err := fs.ReadFile(file, MaxCustomTitleBytes)
	if err != nil {
		return ""
	}
	var v struct {
		CustomTitle any `json:"customTitle"`
	}
	if json.Unmarshal([]byte(raw), &v) != nil {
		return ""
	}
	title, ok := v.CustomTitle.(string)
	if !ok {
		return ""
	}
	return domain.Sanitize(title, domain.DefaultMaxLength)
}

// ListSubagents lists the agent-<id>.jsonl / agent-<id>.meta.json of a subagentsDir returned by
// LocateSessionFiles. Each file must (after symlinks) stay inside subagentsDir itself; ids
// outside the safe charset are ignored.
//
// resolved (optional, one per subagentsDir) holds name to validated path: only new names pay
// realpath + stat, and names gone from the directory leave the map. Only hits are kept, so a
// rejected name (e.g. a stat that lost a race) is tried again.
func ListSubagents(fs FS, subagentsDir string, resolved map[string]string) []SubagentFiles {
	root, ok := NewContainmentRoot(fs, subagentsDir)
	if !ok {
		return nil
	}
	names, err := fs.ReadDir(root.Real)
	if err != nil {
		return nil
	}
	slices.Sort(names)
	realRoot := ContainmentRoot{Dir: root.Real, Real: root.Real}
	byID := map[string]*SubagentFiles{}
	var order []string
	present := map[string]bool{}
	for _, name := range names {
		m := subagentFileRE.FindStringSubmatch(name)
		if m == nil || !IsSafeID(m[1]) {
			continue
		}
		present[name] = true
		file, cached := resolved[name]
		if !cached {
			file, ok = resolveKind(fs, realRoot, filepath.Join(root.Real, name), kindFile)
			if !ok {
				continue
			}
			if resolved != nil {
				resolved[name] = file
			}
		}
		entry := byID[m[1]]
		if entry == nil {
			entry = &SubagentFiles{AgentID: m[1]}
			byID[m[1]] = entry
			order = append(order, m[1])
		}
		if m[2] == ".jsonl" {
			entry.TranscriptPath = file
		} else {
			entry.MetaPath = file
		}
	}
	for name := range resolved {
		if !present[name] {
			delete(resolved, name)
		}
	}
	out := make([]SubagentFiles, len(order))
	for i, id := range order {
		out[i] = *byID[id]
	}
	return out
}
