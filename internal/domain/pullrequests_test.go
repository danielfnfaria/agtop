package domain

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func pr(owner, repo string, number int) PullRequestRef {
	return PullRequestRef{owner, repo, number, fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, number)}
}

func numbers(prs []PullRequestRef) []int {
	out := []int{}
	for _, p := range prs {
		out = append(out, p.Number)
	}
	return out
}

func sameSlice(a, b []PullRequestRef) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

func TestExtractPullRequests(t *testing.T) {
	esc, bel := "\x1b", "\x07"
	t.Run("valid URLs in order with only owner/repo/number/url", func(t *testing.T) {
		text := strings.Join([]string{
			"Opened https://github.com/acme/app/pull/201 and",
			"[#198](https://github.com/acme/app/pull/198).",
			"<https://github.com/Org-1/my.repo_x/pull/7/files>",
			"(see https://github.com/acme/app/pull/190#issuecomment-1)",
			"https://github.com/acme/app/pull/5?w=1, end.",
		}, "\n")
		want := []PullRequestRef{pr("acme", "app", 201), pr("acme", "app", 198), pr("Org-1", "my.repo_x", 7), pr("acme", "app", 190), pr("acme", "app", 5)}
		if got := ExtractPullRequests(text, MaxPullRequests); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	rejects := []struct{ name, text string }{
		{"http, not https", "http://github.com/a/b/pull/1"},
		{"other host", "https://gitlab.com/a/b/pull/1"},
		{"look-alike host", "https://github.com.evil.io/a/b/pull/1"},
		{"subdomain", "https://api.github.com/a/b/pull/1"},
		{"issue, not PR", "https://github.com/a/b/issues/1"},
		{"pulls list", "https://github.com/a/b/pulls"},
		{"zero", "https://github.com/a/b/pull/0"},
		{"leading zero", "https://github.com/a/b/pull/012"},
		{"not a number", "https://github.com/a/b/pull/12abc"},
		{"number glued to a hyphen", "https://github.com/a/b/pull/12-abc"},
		{"negative", "https://github.com/a/b/pull/-1"},
		{"huge number", "https://github.com/a/b/pull/12345678901"},
		{"dot repo", "https://github.com/a/../pull/1"},
		{"owner starting with hyphen", "https://github.com/-a/b/pull/1"},
		{"owner too long", "https://github.com/" + strings.Repeat("a", 40) + "/b/pull/1"},
		{"glued to a word", "xhttps://github.com/a/b/pull/1"},
		{"inside another URL", "https://evil.io/https://github.com/a/b/pull/1"},
		{"encoded", "https://github.com/a%2Fb/c/pull/1"},
		{"unicode in owner", "https://github.com/\u0430cme/b/pull/1"},
		{"escape in repo", "https://github.com/a/b" + esc + "[31m/pull/1"},
		{"uppercase scheme", "HTTPS://github.com/a/b/pull/1"},
	}
	for _, tt := range rejects {
		t.Run("rejects: "+tt.name, func(t *testing.T) {
			if got := ExtractPullRequests(tt.text, MaxPullRequests); len(got) != 0 {
				t.Errorf("got %v, want none", got)
			}
		})
	}

	t.Run("malicious text around a valid URL keeps only the clean parts", func(t *testing.T) {
		text := esc + "]8;;https://evil.io" + bel + "https://github.com/acme/app/pull/42" + esc + "]8;;" + bel + " " + esc + "[2J SECRET"
		got := ExtractPullRequests(text, MaxPullRequests)
		if want := []PullRequestRef{pr("acme", "app", 42)}; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		if s := fmt.Sprintf("%+v", got); strings.Contains(s, "evil") || strings.Contains(s, "SECRET") {
			t.Errorf("leaked: %s", s)
		}
	})

	t.Run("deduplicates case-insensitively, first spelling wins", func(t *testing.T) {
		text := "https://github.com/Acme/App/pull/1 https://github.com/acme/app/pull/1/files https://github.com/acme/app/pull/2"
		want := []PullRequestRef{pr("Acme", "App", 1), pr("acme", "app", 2)}
		if got := ExtractPullRequests(text, MaxPullRequests); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("keeps only the last MaxPullRequests of a text", func(t *testing.T) {
		var parts []string
		for i := 1; i <= 20; i++ {
			parts = append(parts, fmt.Sprintf("https://github.com/a/b/pull/%d", i))
		}
		got := ExtractPullRequests(strings.Join(parts, " "), MaxPullRequests)
		if want := []int{13, 14, 15, 16, 17, 18, 19, 20}; !reflect.DeepEqual(numbers(got), want) {
			t.Errorf("got %v, want %v", numbers(got), want)
		}
	})

	t.Run("text without github.com: nothing", func(t *testing.T) {
		if got := ExtractPullRequests(strings.Repeat("x", 100_000), MaxPullRequests); len(got) != 0 {
			t.Errorf("got %v", got)
		}
	})
}

func TestAddPullRequests(t *testing.T) {
	t.Run("appends new ones in order, keeps place of repeated ones, same list when nothing new", func(t *testing.T) {
		list := []PullRequestRef{pr("a", "b", 1), pr("a", "b", 2)}
		if !sameSlice(AddPullRequests(list, nil, MaxPullRequests), list) {
			t.Error("nil: want the same list")
		}
		if !sameSlice(AddPullRequests(list, []PullRequestRef{}, MaxPullRequests), list) {
			t.Error("empty: want the same list")
		}
		if !sameSlice(AddPullRequests(list, []PullRequestRef{pr("A", "B", 2)}, MaxPullRequests), list) {
			t.Error("repeated: want the same list")
		}
		got := AddPullRequests(list, []PullRequestRef{pr("a", "b", 3), pr("a", "b", 1)}, MaxPullRequests)
		if want := []int{1, 2, 3}; !reflect.DeepEqual(numbers(got), want) {
			t.Errorf("got %v, want %v", numbers(got), want)
		}
	})

	t.Run("limit drops the oldest", func(t *testing.T) {
		var list []PullRequestRef
		for i := 1; i <= 12; i++ {
			list = AddPullRequests(list, []PullRequestRef{pr("a", "b", i)}, MaxPullRequests)
		}
		if want := []int{5, 6, 7, 8, 9, 10, 11, 12}; !reflect.DeepEqual(numbers(list), want) {
			t.Errorf("got %v, want %v", numbers(list), want)
		}
	})
}

func TestUsesGh(t *testing.T) {
	yes := []string{
		"gh pr create --fill", "  gh pr view 12", "cd repo && gh pr create", "git push; gh pr create",
		"false || gh pr list", "echo x | gh pr create --body-file -", "URL=$(gh pr view --json url)",
		"git push\ngh pr create", "gh\tpr list",
	}
	no := []string{"git push", "ghq get x", "echo gh pr create", "cat gh.txt", "gh", "npm run gh", "./gh pr create"}
	for _, c := range yes {
		if !UsesGh(c) {
			t.Errorf("a segment runs gh: %q", c)
		}
	}
	for _, c := range no {
		if UsesGh(c) {
			t.Errorf("no segment runs gh: %q", c)
		}
	}
}

func TestToolResultPullRequests(t *testing.T) {
	prs := []PullRequestRef{pr("acme", "app", 1)}
	for _, tool := range []*PendingTool{{Name: "Bash", UsesGh: true}, {Name: "Agent"}, {Name: "Task"}} {
		if got := ToolResultPullRequests(tool, prs); !sameSlice(got, prs) {
			t.Errorf("%+v: want the PRs kept", tool)
		}
	}
	for _, tool := range []*PendingTool{{Name: "Bash"}, {Name: "Read"}, {Name: "WebFetch"}, nil} {
		if got := ToolResultPullRequests(tool, prs); got != nil {
			t.Errorf("%+v: want the PRs dropped", tool)
		}
	}
}
