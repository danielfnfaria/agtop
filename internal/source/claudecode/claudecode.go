// Package claudecode is Claude Code as a source.Source: it wraps the registry discovery
// (sessions/<pid>.json), the transcript location under projects/, the subagents/ listing and
// the JSONL normalizer.
//
// Still Claude Code specific outside this package: the plan usage file (<dir>/agtop/usage.json,
// read over discovery), the profile resolution in config and the discovery shapes the contract
// exchanges.
package claudecode

import (
	"path/filepath"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/ingest/tailer"
	"github.com/danielfnfaria/agtop/internal/ingest/watcher"
	"github.com/danielfnfaria/agtop/internal/source"
	"github.com/danielfnfaria/agtop/internal/transcript"
)

// Tailer reads the lines appended to a file (see tailer.Tailer).
type Tailer interface {
	Read() (tailer.Result, error)
	Close() error
}

type claudeCode struct {
	deps      discovery.Deps
	newTailer func(path string) (Tailer, error)
}

// New is the Claude Code source over deps (fs, pid checks and caches, injectable in tests).
// newTailer opens the tailer of each transcript file.
func New(deps discovery.Deps, newTailer func(path string) (Tailer, error)) source.Source {
	return claudeCode{deps, newTailer}
}

func (claudeCode) Provider() domain.ProviderID { return domain.ClaudeCode }

func (c claudeCode) Discover(profiles []config.ProfileDir) (discovery.Result, error) {
	return discovery.DiscoverLiveSessions(profiles, c.deps), nil
}

func (c claudeCode) RegistryWatch(profile config.ProfileDir) (watcher.Target, bool) {
	real, err := c.deps.FS.Realpath(filepath.Join(profile.Dir, "sessions"))
	if err != nil {
		return watcher.Target{}, false
	}
	return watcher.Target{Kind: watcher.KindSessions, Root: real}, true
}

func (claudeCode) SessionWatch(transcriptRef, agentsRef string) (watcher.Target, bool) {
	if transcriptRef == "" && agentsRef == "" {
		return watcher.Target{}, false
	}
	return watcher.Target{Kind: watcher.KindSession, TranscriptPath: transcriptRef, SubagentsDir: agentsRef}, true
}

// ForgetSession drops only a found location: a cached miss keeps its TTL.
func (c claudeCode) ForgetSession(profile config.ProfileDir, sessionID string) {
	key := discovery.LocateCacheKey(profile.Dir, sessionID)
	if e, ok := c.deps.LocateCache.Get(key); ok && e.Kind == discovery.LocateFound {
		c.deps.LocateCache.Delete(key)
	}
}

func (c claudeCode) ListAgents(agentsDir string, resolved map[string]string) []discovery.SubagentFiles {
	return discovery.ListSubagents(c.deps.FS, agentsDir, resolved)
}

func (c claudeCode) ReadAgentMeta(file string) discovery.AgentMetaResult {
	return discovery.ReadAgentMeta(c.deps.FS, file)
}

func (c claudeCode) FileSignature(file string) (string, bool) {
	return discovery.StatSignature(c.deps.FS, file)
}

func (c claudeCode) OpenTranscript(ref string) (source.TranscriptReader, error) {
	t, err := c.newTailer(ref)
	if err != nil {
		return nil, err
	}
	return &fileReader{path: ref, tailer: t}, nil
}

// fileReader is a JSONL transcript: a tailer whose lines go through the normalizer.
type fileReader struct {
	path   string
	tailer Tailer
}

func (r *fileReader) Read() (source.TranscriptRead, error) {
	res, err := r.tailer.Read()
	if err != nil {
		return source.TranscriptRead{}, err
	}
	if res.Reset {
		return source.TranscriptRead{Reset: true}, nil
	}
	var out source.TranscriptRead
	if res.DroppedOversizedLines > 0 {
		out.Warnings = []string{"oversized line dropped: " + r.path}
	}
	for _, l := range res.Lines {
		out.Events = append(out.Events, transcript.NormalizeLine(l)...)
	}
	return out, nil
}

func (r *fileReader) Close() error { return r.tailer.Close() }
