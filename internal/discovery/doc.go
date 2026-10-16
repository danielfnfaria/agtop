// Package discovery finds the live Claude Code sessions of each profile: the pid registry
// (sessions/<pid>.json), the transcript and sub-agent files under projects/, and the files the
// status line hook writes under agtop/.
//
// Every file read is capped in size and every path returned stays inside its root after
// symlinks are resolved. Errors and diagnostics carry short codes, never file content.
package discovery
