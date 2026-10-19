package config

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/danielfnfaria/agtop/internal/domain"
)

const (
	home = "/home/u"
	file = "/home/u/.config/agtop/config.toml"
)

func parse(t *testing.T, text string) FileData {
	t.Helper()
	d, err := ParseFile(text, file, nil, home)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return d
}

func TestParseFile(t *testing.T) {
	t.Run("reads the example: profiles in file order", func(t *testing.T) {
		// work < personal alphabetically: the order must come from the file, not the map.
		got := parse(t, "\n[profiles]\nwork = \"~/.claude-work\"\npersonal = \"~/.claude\"\n").Profiles
		want := []ProfileDir{
			{Label: "work", Dir: "/home/u/.claude-work", Source: SourceConfig},
			{Label: "personal", Dir: "/home/u/.claude", Source: SourceConfig},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("order survives quoted keys, dotted keys and inline tables", func(t *testing.T) {
		tests := []struct{ name, text, want string }{
			{"quoted", "[profiles]\n\"z z\" = \"/a\"\n\"a a\" = \"/b\"\n", "z z,a a"},
			{"dotted", "profiles.z = \"/a\"\nprofiles.a = \"/b\"\n", "z,a"},
			{"inline", "profiles = { z = \"/a\", a = \"/b\" }\n", "z,a"},
		}
		for _, tt := range tests {
			var labels []string
			for _, p := range parse(t, tt.text).Profiles {
				labels = append(labels, p.Label)
			}
			if got := strings.Join(labels, ","); got != tt.want {
				t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
			}
		}
	})

	t.Run("accepts an absolute path; trailing separator goes away", func(t *testing.T) {
		tests := []struct{ text, want string }{
			{"[profiles]\na = \"/x/y\"", "/x/y"},
			{"[profiles]\na = \"/x/y/\"", "/x/y"},
			{"[profiles]\na = \"~/c/\"", "/home/u/c"},
		}
		for _, tt := range tests {
			if got := parse(t, tt.text).Profiles[0].Dir; got != tt.want {
				t.Errorf("%q: %q, want %q", tt.text, got, tt.want)
			}
		}
	})

	t.Run("empty file and empty [profiles]: no profiles", func(t *testing.T) {
		for _, text := range []string{"", "[profiles]"} {
			if p := parse(t, text).Profiles; p != nil {
				t.Errorf("%q: %+v", text, p)
			}
		}
	})
}

func TestParseFileErrors(t *testing.T) {
	f := file + ": "
	tests := []struct{ name, text, want string }{
		{"relative path", "[profiles]\na = \"rel/x\"", `profiles.a = "rel/x" is not absolute (use an absolute path or ~)`},
		{"other user's home", "[profiles]\na = \"~other/x\"", "not absolute"},
		{"empty path", "[profiles]\na = \"\"", "profiles.a is an empty path"},
		{"blank path", "[profiles]\na = \"  \"", "profiles.a is an empty path"},
		{"number path", "[profiles]\na = 3", "profiles.a must be a string path"},
		{"array path", "[profiles]\na = [\"x\"]", "profiles.a must be a string path"},
		{"unknown top-level key", "colors = \"dark\"", f + `unknown key "colors" (expected [profiles], [theme], [glyphs], [layout])`},
		{"unknown table", "[other]\nx = 1", `unknown key "other"`},
		{"contextWindows is gone", "[contextWindows]\nm = 5", f + `unknown key "contextWindows"`},
		{"empty label", "[profiles]\n\"\" = \"/x\"", f + "profiles has an empty label"},
		{"blank label", "[profiles]\n\"  \" = \"/x\"", "empty label"},
		{"duplicate key", "[profiles]\na = \"/x\"\na = \"/y\"", f},
		{"control character in label", "[profiles]\n\"a\\u001b[2J\" = \"/x\"", "profiles has a label with control characters"},
		{"label too long", "[profiles]\n\"" + strings.Repeat("a", 65) + "\" = \"/x\"", "profiles has a label longer than 64 characters"},
		{"control character in path", "[profiles]\na = \"/x\\u0007y\"", "profiles.a must not contain control characters"},
		{"control character in dir", "[profiles]\na = { provider = \"claude-code\", dir = \"/x\\u001by\" }", "profiles.a.dir must not contain control characters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { wantFails(t, tt.text, tt.want) })
	}

	t.Run("[profiles] that is not a table", func(t *testing.T) {
		for _, bad := range []string{`"x"`, "1", `["a"]`, "1979-05-27", "1979-05-27T07:32:00Z", "07:32:00", "1979-05-27T07:32:00"} {
			wantFails(t, "profiles = "+bad, f+"[profiles] must be a table")
		}
	})

	t.Run("invalid TOML cites the file and the line", func(t *testing.T) {
		_, err := ParseFile("[profiles]\na = \nb = 1", file, nil, home)
		var ce *Error
		if !errors.As(err, &ce) || !strings.HasPrefix(err.Error(), file+": ") || !strings.Contains(err.Error(), "2| a =") {
			t.Errorf("got %v", err)
		}
	})
}

func TestDefaultConfigPath(t *testing.T) {
	tests := []struct {
		name string
		env  Env
		want string
	}{
		{"XDG_CONFIG_HOME wins", Env{"XDG_CONFIG_HOME": "/xdg"}, "/xdg/agtop/config.toml"},
		{"falls back to ~/.config", Env{}, file},
		{"empty XDG", Env{"XDG_CONFIG_HOME": ""}, file},
		{"relative XDG is ignored", Env{"XDG_CONFIG_HOME": "rel/cfg"}, file},
		{"tilde XDG is ignored", Env{"XDG_CONFIG_HOME": "~/cfg"}, file},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DefaultConfigPath(tt.env, home); got != tt.want {
				t.Errorf("got %q", got)
			}
		})
	}
}

func str(s string) *string { return &s }

func TestLoadFile(t *testing.T) {
	opts := LoadOptions{Env: Env{}, Homedir: home, Cwd: "/work"}

	t.Run("no file at the default path: empty, no error", func(t *testing.T) {
		out, err := LoadFile(nil, newFakeFS(nil, nil, nil), opts)
		if err != nil || out.File != "" || out.Profiles != nil {
			t.Errorf("got %+v, %v", out, err)
		}
	})

	t.Run("reads the default path (XDG vs ~/.config)", func(t *testing.T) {
		fake := newFakeFS(nil, nil, map[string]string{
			file:                     "[profiles]\na = \"/a\"",
			"/xdg/agtop/config.toml": "[profiles]\nb = \"/b\"",
		})
		out, err := LoadFile(nil, fake, opts)
		if err != nil || out.Profiles[0].Label != "a" {
			t.Errorf("got %+v, %v", out, err)
		}
		xdg := opts
		xdg.Env = Env{"XDG_CONFIG_HOME": "/xdg"}
		out, err = LoadFile(nil, fake, xdg)
		if err != nil || out.Profiles[0].Label != "b" || out.File != "/xdg/agtop/config.toml" {
			t.Errorf("got %+v, %v", out, err)
		}
	})

	t.Run("--config is resolved against the cwd; ~ is expanded", func(t *testing.T) {
		fake := newFakeFS(nil, nil, map[string]string{
			"/work/c.toml":   "[profiles]\na = \"/a\"",
			"/home/u/d.toml": "[profiles]\nd = \"/d\"",
		})
		if out, err := LoadFile(str("c.toml"), fake, opts); err != nil || out.File != "/work/c.toml" {
			t.Errorf("got %+v, %v", out, err)
		}
		if out, err := LoadFile(str("~/d.toml"), fake, opts); err != nil || out.Profiles[0].Label != "d" {
			t.Errorf("got %+v, %v", out, err)
		}
	})

	t.Run("--config that does not exist cites the path", func(t *testing.T) {
		_, err := LoadFile(str("nada.toml"), newFakeFS(nil, nil, nil), opts)
		if err == nil || err.Error() != "config file not found: /work/nada.toml" {
			t.Errorf("got %v", err)
		}
	})

	t.Run("a read error becomes an Error with the path", func(t *testing.T) {
		_, err := LoadFile(str("x.toml"), readErrFS{newFakeFS(nil, nil, nil)}, opts)
		if err == nil || err.Error() != "cannot read config file /work/x.toml: EACCES" {
			t.Errorf("got %v", err)
		}
	})

	t.Run("a bad file names itself", func(t *testing.T) {
		fake := newFakeFS(nil, nil, map[string]string{"/work/bad.toml": "x = 1"})
		_, err := LoadFile(str("bad.toml"), fake, opts)
		if err == nil || err.Error() != `/work/bad.toml: unknown key "x" (expected [profiles], [theme], [glyphs], [layout])` {
			t.Errorf("got %v", err)
		}
	})
}

func TestParseFileOpencodeProfiles(t *testing.T) {
	t.Run("a table with provider opencode; dir defaults to the data dir", func(t *testing.T) {
		got := parse(t, "[profiles]\nc = \"~/.claude\"\noc = { provider = \"opencode\" }\n").Profiles
		want := []ProfileDir{
			{Label: "c", Dir: "/home/u/.claude", Source: SourceConfig},
			{Label: "oc", Dir: "/home/u/.local/share/opencode", Source: SourceConfig, Provider: domain.Opencode},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("the default dir follows an absolute XDG_DATA_HOME", func(t *testing.T) {
		d, err := ParseFile("[profiles]\noc = { provider = \"opencode\" }", file, Env{EnvXDGDataHome: "/xdg"}, home)
		if err != nil || d.Profiles[0].Dir != "/xdg/opencode" {
			t.Errorf("got %+v, %v", d.Profiles, err)
		}
	})

	t.Run("explicit dir with ~; claude-code is the default provider", func(t *testing.T) {
		got := parse(t, "[profiles]\na = { provider = \"opencode\", dir = \"~/oc/\" }\nb = { provider = \"claude-code\", dir = \"/x\" }\n").Profiles
		want := []ProfileDir{
			{Label: "a", Dir: "/home/u/oc", Source: SourceConfig, Provider: domain.Opencode},
			{Label: "b", Dir: "/x", Source: SourceConfig},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("errors name the file and the profile", func(t *testing.T) {
		tests := []struct{ text, want string }{
			{"[profiles]\na = {}", file + `: profiles.a needs a provider`},
			{"[profiles]\na = { dir = \"/x\" }", file + `: profiles.a needs a provider`},
			{"[profiles]\na = { provider = \"codex\" }", file + `: profiles.a has an unknown provider "codex"`},
			{"[profiles]\na = { provider = 1 }", file + `: profiles.a.provider must be a string`},
			{"[profiles]\na = { provider = \"opencode\", path = \"/x\" }", file + `: profiles.a has an unknown key "path"`},
			{"[profiles]\na = { provider = \"opencode\", dir = 1 }", file + `: profiles.a.dir must be a string path`},
			{"[profiles]\na = { provider = \"opencode\", dir = \" \" }", file + `: profiles.a.dir is an empty path`},
			{"[profiles]\na = { provider = \"opencode\", dir = \"rel\" }", file + `: profiles.a.dir = "rel" is not absolute`},
			{"[profiles]\na = { provider = \"claude-code\" }", file + `: profiles.a needs a dir`},
			{"[profiles]\na = 1", file + `: profiles.a must be a string path or a table`},
		}
		for _, tt := range tests {
			_, err := ParseFile(tt.text, file, nil, home)
			if err == nil || !strings.HasPrefix(err.Error(), tt.want) {
				t.Errorf("%q: got %v, want prefix %q", tt.text, err, tt.want)
			}
		}
	})
}
