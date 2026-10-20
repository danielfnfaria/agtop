package domain

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// CommandCategory is the category of a shell command (the only thing kept about it).
type CommandCategory string

// Known CommandCategory values.
const (
	CmdTests   CommandCategory = "tests"
	CmdBuild   CommandCategory = "build"
	CmdGit     CommandCategory = "git"
	CmdInstall CommandCategory = "install"
	CmdOther   CommandCategory = "other"
)

// ActivityKind is what an agent is doing, shown as its activity label.
type ActivityKind string

// Known ActivityKind values.
const (
	KindWriting    ActivityKind = "writing"
	KindReading    ActivityKind = "reading"
	KindSearching  ActivityKind = "searching"
	KindWeb        ActivityKind = "web"
	KindDelegating ActivityKind = "delegating"
	KindTests      ActivityKind = "tests"
	KindBuilding   ActivityKind = "building"
	KindGit        ActivityKind = "git"
	KindInstalling ActivityKind = "installing"
	KindBash       ActivityKind = "bash"
	KindTool       ActivityKind = "tool"
	KindThinking   ActivityKind = "thinking"
	KindStarting   ActivityKind = "starting"
)

// AgentActivity is what an agent is doing right now.
type AgentActivity struct {
	Kind ActivityKind
	// Tool is only for KindTool: the (already sanitized) tool name.
	Tool string
}

// maxCommand is the longest command prefix inspected (classification needs only the start of
// each segment).
const maxCommand = 4_000

const pm = `(?:npm|pnpm|yarn|bun)`

var (
	// Separators between the commands of a shell line (&&, ||, ;, |, newline).
	segmentSplitRE = regexp.MustCompile(`&&|\|\||[;|\n]`)
	// Leading VAR=value assignments.
	envAssignRE = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_]*=(?:"[^"]*"|'[^']*'|` + JSNotWS + `*)` + JSWS + `+)+`)
	// Wrappers that run the real command next.
	wrapperRE = regexp.MustCompile(`^(?:sudo|time|nice|nohup|command|exec|env|timeout` + JSWS + `+` + JSNotWS + `+|xargs)` + JSWS + `+`)
	// Runners that execute a package binary.
	runnerRE  = regexp.MustCompile(`^(?:npx|bunx|pnpx|pnpm` + JSWS + `+exec|pnpm` + JSWS + `+dlx|yarn` + JSWS + `+dlx|yarn` + JSWS + `+exec|bundle` + JSWS + `+exec|uv` + JSWS + `+run|poetry` + JSWS + `+run)` + JSWS + `+`)
	leadingRE = regexp.MustCompile(`^[({` + jsWSChars + `]+`)

	testsREs = compileAll(
		`^`+pm+JSWS+`+(?:run`+JSWS+`+)?(?:test|t)(?::`+JSNotWS+`+)?(?:`+JSWS+`|$)`,
		`^(?:vitest|jest|mocha|ava|tap|karma|jasmine|cypress`+JSWS+`+run|playwright`+JSWS+`+test)\b`,
		`^(?:python3?`+JSWS+`+-m`+JSWS+`+)?(?:pytest|tox|nox|unittest)\b`,
		`^(?:go|dotnet|deno|bun|swift|mix|flutter|dart)`+JSWS+`+test\b`,
		`^cargo`+JSWS+`+(?:test|nextest)\b`,
		`^(?:make|just)`+JSWS+`+(?:test|tests|check)\b`,
		`^(?:\./)?(?:gradlew|gradle|mvnw|mvn)\b.*\b(?:test|check|verify)\b`,
		`^(?:rspec|phpunit|ctest)\b`,
		`^(?:rake|rails)`+JSWS+`+(?:test|spec)\b`,
	)
	buildREs = compileAll(
		`^`+pm+JSWS+`+(?:run`+JSWS+`+)?(?:build|typecheck|type-check|lint|check|compile|tsc|format|fmt)(?::`+JSNotWS+`+)?(?:`+JSWS+`|$)`,
		`^(?:tsc|eslint|prettier|biome|oxlint|vite`+JSWS+`+build|webpack|rollup|esbuild|turbo|nx)\b`,
		`^(?:make|cmake|ninja|bazel|just)\b`,
		`^cargo`+JSWS+`+(?:build|check|clippy|fmt)\b`,
		`^go`+JSWS+`+(?:build|vet|fmt|generate)\b`,
		`^(?:gofmt|goimports|golangci-lint|staticcheck)\b`,
		`^dotnet`+JSWS+`+(?:build|format|publish|pack)\b`,
		`^(?:\./)?(?:gradlew|gradle|mvnw|mvn)\b`,
		`^(?:ruff|mypy|pyright|black|flake8|pylint|isort)\b`,
		`^docker`+JSWS+`+(?:build|compose`+JSWS+`+build)\b`,
		`^terraform`+JSWS+`+(?:fmt|validate)\b`,
		`^swift`+JSWS+`+build\b`,
	)
	installREs = compileAll(
		`^`+pm+JSWS+`+(?:i|install|add|ci)(?:`+JSWS+`|$)`,
		`^yarn$`,
		`^(?:pip3?|pipx|uv`+JSWS+`+pip|python3?`+JSWS+`+-m`+JSWS+`+pip)`+JSWS+`+install\b`,
		`^uv`+JSWS+`+(?:add|sync)\b`,
		`^poetry`+JSWS+`+(?:install|add)\b`,
		`^brew`+JSWS+`+(?:install|upgrade|update|tap|bundle)\b`,
		`^(?:apt|apt-get|dnf|yum|apk|pacman|port)`+JSWS+`+(?:install|add|-S)\b`,
		`^go`+JSWS+`+(?:get|install|mod`+JSWS+`+(?:download|tidy))\b`,
		`^cargo`+JSWS+`+(?:install|add|fetch)\b`,
		`^dotnet`+JSWS+`+(?:restore|add`+JSWS+`+package|tool`+JSWS+`+install)\b`,
		`^gem`+JSWS+`+install\b`,
		`^bundle(?:`+JSWS+`+install)?$`,
		`^composer`+JSWS+`+(?:install|require)\b`,
	)
	gitRE = regexp.MustCompile(`^(?:git|gh)(?:` + JSWS + `|$)`)
)

func compileAll(patterns ...string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(patterns))
	for i, p := range patterns {
		out[i] = regexp.MustCompile(p)
	}
	return out
}

func matchAny(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// commandHead strips env assignments, wrappers and package runners from the start of a command.
func commandHead(segment string) string {
	s := leadingRE.ReplaceAllString(strings.TrimFunc(segment, isSpace), "")
	for range 6 {
		next := runnerRE.ReplaceAllString(wrapperRE.ReplaceAllString(envAssignRE.ReplaceAllString(s, ""), ""), "")
		if next == s {
			break
		}
		s = next
	}
	return s
}

var categoryRank = map[CommandCategory]int{CmdOther: 0, CmdGit: 1, CmdInstall: 2, CmdBuild: 3, CmdTests: 4}

func segmentCategory(head string) CommandCategory {
	switch {
	case matchAny(testsREs, head):
		return CmdTests
	case matchAny(buildREs, head):
		return CmdBuild
	case matchAny(installREs, head):
		return CmdInstall
	case gitRE.MatchString(head):
		return CmdGit
	}
	return CmdOther
}

// ClassifyCommand returns the category of a shell command line. Each command of the line (split
// on &&, ||, ; and |) is classified and the most telling one wins: tests > build > install >
// git > other (e.g. `npm run build && npm test` is running tests).
func ClassifyCommand(command string) CommandCategory {
	if len(command) > maxCommand {
		command = command[:maxCommand]
		for !utf8.ValidString(command) && len(command) > 0 {
			command = command[:len(command)-1]
		}
	}
	best := CmdOther
	for _, segment := range segmentSplitRE.Split(command, -1) {
		if c := segmentCategory(commandHead(segment)); categoryRank[c] > categoryRank[best] {
			best = c
		}
	}
	return best
}

var commandActivity = map[CommandCategory]ActivityKind{
	CmdTests:   KindTests,
	CmdBuild:   KindBuilding,
	CmdGit:     KindGit,
	CmdInstall: KindInstalling,
	CmdOther:   KindBash,
}

var toolActivityKinds = map[string]ActivityKind{
	"Edit":         KindWriting,
	"Write":        KindWriting,
	"MultiEdit":    KindWriting,
	"NotebookEdit": KindWriting,
	"Read":         KindReading,
	"Grep":         KindSearching,
	"Glob":         KindSearching,
	"WebFetch":     KindWeb,
	"WebSearch":    KindWeb,
	"Agent":        KindDelegating,
	"Task":         KindDelegating,
}

// ToolActivity returns the activity of one pending tool.
func ToolActivity(tool PendingTool) AgentActivity {
	if tool.Name == "Bash" {
		if k, ok := commandActivity[tool.CommandCategory]; ok {
			return AgentActivity{Kind: k}
		}
		return AgentActivity{Kind: KindBash}
	}
	if k, ok := toolActivityKinds[tool.Name]; ok {
		return AgentActivity{Kind: k}
	}
	return AgentActivity{Kind: KindTool, Tool: tool.Name}
}

// CurrentAgentActivity returns what an active agent is doing: its most recent pending tool;
// without one, thinking once it runs and starting right after the launch. It returns nil for an
// agent that is not active.
func CurrentAgentActivity(status AgentStatus, pendingTools []PendingEntry) *AgentActivity {
	if !IsActive(status) {
		return nil
	}
	if n := len(pendingTools); n > 0 {
		a := ToolActivity(pendingTools[n-1].PendingTool)
		return &a
	}
	if status == StatusLaunched {
		return &AgentActivity{Kind: KindStarting}
	}
	return &AgentActivity{Kind: KindThinking}
}
