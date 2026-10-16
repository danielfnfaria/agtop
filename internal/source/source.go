// Package source is the boundary between the store and where sessions come from. A Source is
// everything the store needs from one provider: find the live sessions of each profile, tell
// when that set may have changed, list and read the agent files of a session and turn
// transcript lines into domain events.
//
// The shapes it exchanges (discovery.Result, SessionFiles, SubagentFiles) are the Claude Code
// ones: transcripts are JSONL files read by tailers and agents live in a subagents/ directory.
// A source whose sessions do not fit that (a database, say) needs a wider contract.
package source

import (
	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
)

// Source provides the sessions of one provider.
type Source interface {
	// Provider is the provider of every session and profile this source reports.
	Provider() domain.ProviderID
	// Discover scans every profile once: live sessions (one per session id) with their files,
	// the inactive ones and per-profile diagnostics.
	Discover(profiles []config.ProfileDir) (discovery.Result, error)
	// RegistryRoot is the canonical directory whose changes mean the live set of profile may
	// have changed (the store watches it and scans on any event); false when it does not exist
	// yet.
	RegistryRoot(profile config.ProfileDir) (string, bool)
	// ForgetSession drops what the source cached about a session that left (e.g. where its
	// files are).
	ForgetSession(profile config.ProfileDir, sessionID string)
	// ListAgents lists the validated agent files in the agents directory of a session
	// (SessionFiles.SubagentsDir). resolved (one per directory, kept by the store) caches name
	// to path between calls.
	ListAgents(agentsDir string, resolved map[string]string) []discovery.SubagentFiles
	// ReadAgentMeta reads an agent metadata file returned by ListAgents.
	ReadAgentMeta(file string) discovery.AgentMetaResult
	// FileSignature is a cheap change signature of a file ("size:mtimeMs"); false if unknown.
	FileSignature(file string) (string, bool)
	// ParseLine turns one transcript line into domain events (none for lines the source
	// ignores).
	ParseLine(line string) []domain.TranscriptEvent
}
