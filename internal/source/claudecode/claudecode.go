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
	"github.com/danielfnfaria/agtop/internal/source"
	"github.com/danielfnfaria/agtop/internal/transcript"
)

type claudeCode struct {
	deps discovery.Deps
}

// New is the Claude Code source over deps (fs, pid checks and caches, injectable in tests).
func New(deps discovery.Deps) source.Source { return claudeCode{deps} }

func (claudeCode) Provider() domain.ProviderID { return domain.ClaudeCode }

func (c claudeCode) Discover(profiles []config.ProfileDir) (discovery.Result, error) {
	return discovery.DiscoverLiveSessions(profiles, c.deps), nil
}

func (c claudeCode) RegistryRoot(profile config.ProfileDir) (string, bool) {
	real, err := c.deps.FS.Realpath(filepath.Join(profile.Dir, "sessions"))
	return real, err == nil
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

func (claudeCode) ParseLine(line string) []domain.TranscriptEvent {
	return transcript.NormalizeLine(line)
}
