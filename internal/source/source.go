// Package source is the boundary between the store and where sessions come from. A Source is
// everything the store needs from one provider: find the live sessions of each profile, tell
// when that set may have changed, open the transcript of a session, list and read its agent
// files and say what to watch.
//
// Transcripts and watch targets are opaque to the store: a source hands out TranscriptReaders
// that yield domain events and watcher targets, so a provider whose sessions live in a database
// fits as well as one that reads files. The discovery shapes it exchanges (discovery.Result,
// SessionFiles, SubagentFiles) are still the Claude Code ones.
package source

import (
	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/ingest/watcher"
)

// TranscriptRead is what one Read of a transcript found.
type TranscriptRead struct {
	// Events are the new domain events, in order (none for what the source ignores).
	Events []domain.TranscriptEvent
	// Reset: the transcript was truncated, replaced or removed; the consumer must drop what it
	// reduced and read again from the start. Events and Warnings are empty then.
	Reset bool
	// Warnings are problems of this read that did not stop it (e.g. a line dropped); each one is
	// shown as an error of the store.
	Warnings []string
}

// TranscriptReader reads a transcript incrementally: each Read returns what grew since the
// last one.
type TranscriptReader interface {
	Read() (TranscriptRead, error)
	Close() error
}

// Source provides the sessions of one provider.
type Source interface {
	// Provider is the provider of every session and profile this source reports.
	Provider() domain.ProviderID
	// Discover scans the given profiles once (all of this provider): live sessions (one per
	// session id) with their files, the inactive ones and per-profile diagnostics.
	Discover(profiles []config.ProfileDir) (discovery.Result, error)
	// RegistryWatch is the target whose events mean the live set of profile may have changed
	// (the store watches it and scans on any event); false when there is nothing to watch yet.
	RegistryWatch(profile config.ProfileDir) (watcher.Target, bool)
	// SessionWatch is the target for the files of one live session (its transcript and agents
	// directory, as in SessionFiles); false when there is nothing to watch.
	SessionWatch(transcriptRef, agentsRef string) (watcher.Target, bool)
	// ForgetSession drops what the source cached about a session that left (e.g. where its
	// files are).
	ForgetSession(profile config.ProfileDir, sessionID string)
	// OpenTranscript opens a transcript returned by Discover or ListAgents (a path, for file
	// sources). The caller closes it.
	OpenTranscript(ref string) (TranscriptReader, error)
	// ListAgents lists the validated agent files in the agents directory of a session
	// (SessionFiles.SubagentsDir). resolved (one per directory, kept by the store) caches name
	// to path between calls.
	ListAgents(agentsDir string, resolved map[string]string) []discovery.SubagentFiles
	// ReadAgentMeta reads an agent metadata file returned by ListAgents.
	ReadAgentMeta(file string) discovery.AgentMetaResult
	// FileSignature is a cheap change signature of a file ("size:mtimeMs"); false if unknown.
	FileSignature(file string) (string, bool)
}
