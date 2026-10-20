package domain

import (
	"regexp"
	"strconv"
	"strings"
)

// PullRequestRef is a GitHub PR link, rebuilt from its validated parts.
type PullRequestRef struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	// URL is https://github.com/<owner>/<repo>/pull/<number>.
	URL string `json:"url"`
}

// MaxPullRequests is how many PRs a session (or agent) keeps: the most recent ones.
const MaxPullRequests = 8

// maxScan is the longest text scanned per block.
const maxScan = 1_000_000

// prURLRE finds candidates; prBoundaryBefore and the checks in ExtractPullRequests add what RE2
// cannot express. The owner is as GitHub allows (alphanumerics and hyphens, up to 39, not
// starting with a hyphen), the repo is [A-Za-z0-9._-], the number a positive integer. A
// candidate is rejected when preceded by a URL/word character (no `xhttps://`, no
// `evil.com/https://...`) or followed by a word character or hyphen (no `pull/12abc`).
var prURLRE = regexp.MustCompile(`https://github\.com/([A-Za-z0-9][A-Za-z0-9-]{0,38})/([A-Za-z0-9._-]{1,100})/pull/([1-9][0-9]*)`)

func isPRBeforeChar(b byte) bool {
	return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' ||
		b == '_' || b == '.' || b == '/' || b == '%' || b == '-'
}

func isPRAfterChar(b byte) bool {
	return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_' || b == '-'
}

// agentTools launch a sub-agent: their result is the agent's report (it may name its PRs).
var agentTools = map[string]bool{"Agent": true, "Task": true}

// commandSeparatorRE separates the segments of a shell line (`$(` and subshells via `(`).
var commandSeparatorRE = regexp.MustCompile("&&|\\|\\||[;|&\n(`]")

var ghSegmentRE = regexp.MustCompile(`^gh` + JSWS)

// UsesGh reports whether some segment of a shell command starts with `gh` (e.g. `gh pr create`,
// `cd x && gh pr view`). Only this boolean is kept: never the command itself.
func UsesGh(command string) bool {
	for _, segment := range commandSeparatorRE.Split(command, -1) {
		if ghSegmentRE.MatchString(strings.TrimLeftFunc(segment, isSpace)) {
			return true
		}
	}
	return false
}

// ToolResultPullRequests returns the PR links of a tool result worth keeping: only from tools
// whose output reports PRs (a Bash running `gh`, or a sub-agent's report). tool is the pending
// tool the result answers (nil if unknown).
func ToolResultPullRequests(tool *PendingTool, prs []PullRequestRef) []PullRequestRef {
	if tool == nil || prs == nil {
		return nil
	}
	if tool.UsesGh || agentTools[tool.Name] {
		return prs
	}
	return nil
}

func prKey(pr PullRequestRef) string {
	return strings.ToLower(pr.Owner) + "/" + strings.ToLower(pr.Repo) + "#" + strconv.Itoa(pr.Number)
}

func lastN(prs []PullRequestRef, limit int) []PullRequestRef {
	if len(prs) > limit {
		return prs[len(prs)-limit:]
	}
	return prs
}

// ExtractPullRequests returns the PR references in text, in order of appearance, deduplicated,
// at most limit (the last ones).
func ExtractPullRequests(text string, limit int) []PullRequestRef {
	if !strings.Contains(text, "github.com/") {
		return []PullRequestRef{}
	}
	if len(text) > maxScan {
		text = text[:maxScan]
	}
	found := []PullRequestRef{}
	seen := map[string]bool{}
	for _, m := range prURLRE.FindAllStringSubmatchIndex(text, -1) {
		if m[0] > 0 && isPRBeforeChar(text[m[0]-1]) {
			continue
		}
		if m[1] < len(text) && isPRAfterChar(text[m[1]]) {
			continue
		}
		owner, repo, num := text[m[2]:m[3]], text[m[4]:m[5]], text[m[6]:m[7]]
		if strings.Trim(repo, ".") == "" || len(num) > 9 {
			continue
		}
		n, _ := strconv.Atoi(num)
		pr := PullRequestRef{Owner: owner, Repo: repo, Number: n, URL: "https://github.com/" + owner + "/" + repo + "/pull/" + num}
		k := prKey(pr)
		if seen[k] {
			continue
		}
		seen[k] = true
		found = append(found, pr)
	}
	return lastN(found, limit)
}

// AddPullRequests returns list plus the PRs of more not in it yet, keeping the order of
// appearance (a PR seen again keeps its place) and only the last limit. It returns list itself
// when nothing is new.
func AddPullRequests(list, more []PullRequestRef, limit int) []PullRequestRef {
	if len(more) == 0 {
		return list
	}
	keys := make(map[string]bool, len(list))
	for _, pr := range list {
		keys[prKey(pr)] = true
	}
	var fresh []PullRequestRef
	for _, pr := range more {
		k := prKey(pr)
		if keys[k] {
			continue
		}
		keys[k] = true
		fresh = append(fresh, pr)
	}
	if len(fresh) == 0 {
		return list
	}
	all := make([]PullRequestRef, 0, len(list)+len(fresh))
	all = append(append(all, list...), fresh...)
	return lastN(all, limit)
}
