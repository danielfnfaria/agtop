package config

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const configPath = "/home/u/.config/agtop/config.toml"

func resolve(fsys FS, mutate func(*ResolveInput)) ([]ProfileDir, error) {
	in := ResolveInput{Env: Env{}, Homedir: home, FS: fsys, Cwd: "/work", ConfigPath: configPath}
	if mutate != nil {
		mutate(&in)
	}
	return ResolveClaudeDirs(in)
}

func prof(label, dir string) ProfileDir {
	return ProfileDir{Label: label, Dir: dir, Source: SourceConfig}
}

func TestResolveFileProfiles(t *testing.T) {
	t.Run("uses the file's profiles, in order, ignoring CLAUDE_CONFIG_DIR and ~/.claude", func(t *testing.T) {
		profiles := []ProfileDir{prof("b", "/x/b"), prof("a", "/x/a")}
		fake := newFakeFS([]string{"/home/u/.claude/projects", "/ccd/projects"}, nil, nil)
		got, err := resolve(fake, func(in *ResolveInput) {
			in.Profiles = profiles
			in.Env = Env{"CLAUDE_CONFIG_DIR": "/ccd"}
		})
		if err != nil || !reflect.DeepEqual(got, profiles) {
			t.Errorf("got %+v, %v", got, err)
		}
	})

	t.Run("does not fail on a missing directory (validation reports it)", func(t *testing.T) {
		p := []ProfileDir{prof("p", "/nope")}
		got, err := resolve(newFakeFS(nil, nil, nil), func(in *ResolveInput) { in.Profiles = p })
		if err != nil || !reflect.DeepEqual(got, p) {
			t.Errorf("got %+v, %v", got, err)
		}
	})

	t.Run("two labels for the same directory are an error", func(t *testing.T) {
		fake := newFakeFS([]string{"/real/.claude/projects"}, map[string]string{"/home/u/link": "/real/.claude"}, nil)
		tests := []struct {
			name     string
			profiles []ProfileDir
		}{
			{"symlink", []ProfileDir{prof("a", "/real/.claude"), prof("b", "/home/u/link")}},
			{"same path", []ProfileDir{prof("a", "/real/.claude"), prof("b", "/real/.claude")}},
			{"trailing separator", []ProfileDir{prof("a", "/real/.claude"), prof("b", "/real/.claude/")}},
		}
		for _, tt := range tests {
			_, err := resolve(fake, func(in *ResolveInput) { in.Profiles = tt.profiles })
			var ce *Error
			if !errors.As(err, &ce) || !strings.Contains(err.Error(), `profiles "a" and "b" point to the same directory (`) {
				t.Errorf("%s: got %v", tt.name, err)
			}
		}
	})

	t.Run("empty profiles fall back", func(t *testing.T) {
		got, err := resolve(newFakeFS([]string{"/home/u/.claude/projects"}, nil, nil), func(in *ResolveInput) { in.Profiles = []ProfileDir{} })
		if err != nil || got[0].Source != SourceDefault {
			t.Errorf("got %+v, %v", got, err)
		}
	})
}

func TestResolveFallback(t *testing.T) {
	tests := []struct {
		name string
		dirs []string
		env  Env
		want ProfileDir
	}{
		{"CLAUDE_CONFIG_DIR beats ~/.claude", []string{"/home/u/.claude/projects", "/ccd/.claude-c/projects"},
			Env{"CLAUDE_CONFIG_DIR": "/ccd/.claude-c"}, ProfileDir{"claude-c", "/ccd/.claude-c", SourceClaudeConfigDir}},
		{"nothing set: only ~/.claude, no search", []string{"/home/u/.claude/projects", "/home/u/.claude-x/projects"},
			Env{}, ProfileDir{"claude", "/home/u/.claude", SourceDefault}},
		{"empty CLAUDE_CONFIG_DIR falls back", []string{"/home/u/.claude/projects"},
			Env{"CLAUDE_CONFIG_DIR": ""}, ProfileDir{"claude", "/home/u/.claude", SourceDefault}},
		{"relative CLAUDE_CONFIG_DIR is relative to the cwd", []string{"/work/cc/projects"},
			Env{"CLAUDE_CONFIG_DIR": "cc"}, ProfileDir{"cc", "/work/cc", SourceClaudeConfigDir}},
		{"~ expands in CLAUDE_CONFIG_DIR", []string{"/home/u/projects"},
			Env{"CLAUDE_CONFIG_DIR": "~"}, ProfileDir{"u", "/home/u", SourceClaudeConfigDir}},
		{"~/.claude without projects/ stays (validation reports it)", []string{"/home/u/.claude/algo"},
			Env{}, ProfileDir{"claude", "/home/u/.claude", SourceDefault}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolve(newFakeFS(tt.dirs, nil, nil), func(in *ResolveInput) { in.Env = tt.env })
			if err != nil || !reflect.DeepEqual(got, []ProfileDir{tt.want}) {
				t.Errorf("got %+v, %v", got, err)
			}
		})
	}

	t.Run("missing ~/.claude says where it looked and suggests [profiles]", func(t *testing.T) {
		_, err := resolve(newFakeFS([]string{"/home/u/.claude-x/projects"}, nil, nil), nil)
		want := "no Claude Code config directory found at /home/u/.claude. List the directories under [profiles] in " + configPath
		if err == nil || err.Error() != want {
			t.Errorf("got %v", err)
		}
	})

	t.Run("missing CLAUDE_CONFIG_DIR is an error (no fallback to ~/.claude)", func(t *testing.T) {
		_, err := resolve(newFakeFS([]string{"/home/u/.claude/projects"}, nil, nil), func(in *ResolveInput) {
			in.Env = Env{"CLAUDE_CONFIG_DIR": "/nao/existe"}
		})
		want := "no Claude Code config directory found at CLAUDE_CONFIG_DIR=/nao/existe. List the directories under [profiles] in " + configPath
		if err == nil || err.Error() != want {
			t.Errorf("got %v", err)
		}
	})
}

func TestExpandHome(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/a/~/b", "/a/~/b"}, {"~other/x", "~other/x"}, {"~/x", "/home/u/x"}, {"~", home},
	}
	for _, tt := range tests {
		if got := ExpandHome(tt.in, home); got != tt.want {
			t.Errorf("ExpandHome(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
