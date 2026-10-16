// Package domain is the agtop domain model: normalized transcript events, the state derived
// from a session and its sub-agents, and the pure rules over them. Nothing here does I/O.
//
// Nothing here keeps a prompt, command, full tool input, stdout or tool_result content: only
// metadata (names, ids, counts, file paths and short sanitized descriptions).
//
// Optional numbers that are epoch milliseconds use 0 for "absent"; the other optional numbers
// are pointers. JSON field names match the TypeScript implementation.
package domain
