package domain

import (
	"strings"
	"testing"
)

func TestClassifyCommand(t *testing.T) {
	cases := map[CommandCategory][]string{
		CmdTests: {
			"npm test", "npm t", "npm run test", "npm run test:unit -- --watch=false", "pnpm test", "yarn test",
			"bun test", "npx vitest run test/ui", "vitest run", "jest --ci", "pytest -q tests/", "python -m pytest",
			"go test ./...", "dotnet test", "cargo test --all", "cargo nextest run", "make test", "./gradlew test",
			"mvn -q verify", "cd app && npm test 2>&1 | tail -20", "CI=1 FORCE_COLOR=0 npm test",
			"timeout 600 npm test", "npm run build && npm test",
		},
		CmdBuild: {
			"npm run build", "npm run typecheck", "npm run lint", "pnpm build", "tsc -p tsconfig.json --noEmit",
			"npx tsc --noEmit", "npx eslint .", "prettier --check .", "make", "make all", "cargo build --release",
			"cargo clippy", "go build ./...", "go vet ./...", "dotnet build", "golangci-lint run", "ruff check .",
			"docker build -t x .", "npm run lint && npm run typecheck",
		},
		CmdGit: {"git status", "git -C repo log --oneline", "gh pr create --fill", "cd x && git diff"},
		CmdInstall: {
			"npm i", "npm install --save-dev vitest", "npm ci", "pnpm add react", "yarn", "pip install -r requirements.txt",
			"pip3 install x", "uv pip install x", "brew install jq", "go get example.com/x", "go mod tidy",
			"cargo add serde", "dotnet restore", "sudo apt-get install -y curl",
		},
		CmdOther: {
			"ls -la", "cat package.json", "curl https://example.invalid", "echo npm test", "node script.js",
			"rm -rf dist", "", "npm run dev", "gitk", "testing-tool",
		},
	}
	for category, commands := range cases {
		for _, command := range commands {
			t.Run(string(category)+": "+command, func(t *testing.T) {
				if got := ClassifyCommand(command); got != category {
					t.Errorf("got %s, want %s", got, category)
				}
			})
		}
	}

	t.Run("git loses to a more telling command in the same line", func(t *testing.T) {
		if got := ClassifyCommand("git stash && npm test"); got != CmdTests {
			t.Errorf("got %s", got)
		}
		if got := ClassifyCommand("npm install && git status"); got != CmdInstall {
			t.Errorf("got %s", got)
		}
	})

	t.Run("a huge command does not hang", func(t *testing.T) {
		if got := ClassifyCommand("echo " + strings.Repeat("a", 5_000_000)); got != CmdOther {
			t.Errorf("got %s", got)
		}
	})
}

func TestToolActivity(t *testing.T) {
	kinds := []struct {
		name string
		kind ActivityKind
	}{
		{"Edit", KindWriting}, {"Write", KindWriting}, {"MultiEdit", KindWriting}, {"NotebookEdit", KindWriting},
		{"Read", KindReading}, {"Grep", KindSearching}, {"Glob", KindSearching}, {"WebFetch", KindWeb},
		{"WebSearch", KindWeb}, {"Agent", KindDelegating}, {"Task", KindDelegating},
	}
	for _, tt := range kinds {
		if got := ToolActivity(PendingTool{Name: tt.name}); got != (AgentActivity{Kind: tt.kind}) {
			t.Errorf("%s: got %+v", tt.name, got)
		}
	}
	bash := []struct {
		category CommandCategory
		kind     ActivityKind
	}{
		{CmdTests, KindTests}, {CmdBuild, KindBuilding}, {CmdGit, KindGit}, {CmdInstall, KindInstalling}, {CmdOther, KindBash},
		{"", KindBash},
	}
	for _, tt := range bash {
		if got := ToolActivity(PendingTool{Name: "Bash", CommandCategory: tt.category}); got != (AgentActivity{Kind: tt.kind}) {
			t.Errorf("Bash %q: got %+v", tt.category, got)
		}
	}
	if got := ToolActivity(PendingTool{Name: "mcp__srv__do"}); got != (AgentActivity{Kind: KindTool, Tool: "mcp__srv__do"}) {
		t.Errorf("unknown tool: got %+v", got)
	}
}

func TestCurrentAgentActivity(t *testing.T) {
	pending := []PendingEntry{
		{"t1", PendingTool{Name: "Read"}},
		{"t2", PendingTool{Name: "Bash", CommandCategory: CmdTests}},
	}
	if got := CurrentAgentActivity(StatusRunning, pending); got == nil || *got != (AgentActivity{Kind: KindTests}) {
		t.Errorf("most recent pending tool: got %v", got)
	}
	if got := CurrentAgentActivity(StatusRunning, nil); got == nil || got.Kind != KindThinking {
		t.Errorf("running without tools: got %v", got)
	}
	if got := CurrentAgentActivity(StatusLaunched, nil); got == nil || got.Kind != KindStarting {
		t.Errorf("launched without tools: got %v", got)
	}
	for _, s := range []AgentStatus{StatusCompleted, StatusFailed, StatusHandedBack, StatusUnknown} {
		if got := CurrentAgentActivity(s, pending); got != nil {
			t.Errorf("%s: got %v, want nil", s, got)
		}
	}
}
