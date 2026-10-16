package model

import (
	"time"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/store"
)

const now int64 = 1_700_000_000_000

var utc = time.UTC

var profiles = []config.ProfileDir{
	{Label: "claude", Dir: "/tmp/a/.claude", Source: config.ProfileSource("default")},
	{Label: "claude-x", Dir: "/tmp/b/.claude-x", Source: config.ProfileSource("config")},
}

func profileStatus(label, dir string) store.ProfileStatus {
	return store.ProfileStatus{Label: label, Provider: domain.ClaudeCode, Dir: dir, Issues: map[discovery.IssueReason]int{}}
}

func view(key string, activity discovery.Activity, agents ...domain.AgentStatus) *store.SessionView {
	s := &store.SessionView{Key: key, Activity: activity, Agents: map[string]*store.AgentView{}}
	for i, st := range agents {
		s.Agents[string(rune('a'+i))] = &store.AgentView{Status: st}
	}
	return s
}
