package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRenderInitialConfig(t *testing.T) {
	render := func(p ProfileDir, hints ...ProfileDir) string {
		return RenderInitialConfig(InitialConfigInput{Profile: p, Hints: hints, Homedir: home})
	}

	t.Run("header, [profiles] with ~ collapsed and commented hints", func(t *testing.T) {
		text := render(prof("claude", home+"/.claude"), prof("claude-work", home+"/.claude-work"))
		for _, want := range []string{
			"takes precedence over CLAUDE_CONFIG_DIR", "README",
			"\n[profiles]\nclaude = \"~/.claude\"\n", "\n# claude-work = \"~/.claude-work\"\n",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("missing %q in\n%s", want, text)
			}
		}
	})

	t.Run("no hints section without hints", func(t *testing.T) {
		if strings.Contains(render(prof("claude", home+"/.claude")), "Other directories") {
			t.Error("unexpected hints section")
		}
	})

	t.Run("CLAUDE_CONFIG_DIR source adds a note", func(t *testing.T) {
		p := ProfileDir{Label: "c", Dir: "/x", Source: SourceClaudeConfigDir}
		if !strings.Contains(render(p), "came from CLAUDE_CONFIG_DIR, which no longer applies") {
			t.Error("missing note")
		}
	})

	tests := []struct {
		name string
		p    ProfileDir
		want string
	}{
		{"quotes labels that are not bare keys; outside home stays absolute", prof("my dir", `/opt/claude "x"`), `"my dir" = "/opt/claude \"x\""`},
		{"does not collapse a sibling prefix of home", prof("c", "/home/u2/.claude"), `c = "/home/u2/.claude"`},
		{"home itself is not collapsed", prof("c", home), `c = "/home/u"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if text := render(tt.p); !strings.Contains(text, tt.want) {
				t.Errorf("missing %s in\n%s", tt.want, text)
			}
		})
	}

	t.Run("round-trips through ParseFile", func(t *testing.T) {
		for _, p := range []ProfileDir{
			prof("my dir", `/opt/claude "x"`),
			prof("a\x7fb", "/opt/x\x7fy"),
			prof("tab\tq\\", "/opt/\né"),
			prof("claude", home+"/.claude"),
		} {
			text := render(p, prof("claude-work", home+"/.claude-work"))
			d, err := ParseFile(text, "c.toml", home)
			if err != nil || !reflect.DeepEqual(d.Profiles, []ProfileDir{p}) {
				t.Errorf("%+v: got %+v, %v\n%s", p, d.Profiles, err, text)
			}
		}
	})
}

func TestFindSiblingProfiles(t *testing.T) {
	valid := func(name string) []string { return []string{home + "/" + name, home + "/" + name + "/projects"} }
	labels := func(ps []ProfileDir) []string {
		var out []string
		for _, p := range ps {
			out = append(out, p.Label)
		}
		return out
	}
	cat := func(lists ...[]string) []string {
		var out []string
		for _, l := range lists {
			out = append(out, l...)
		}
		return out
	}

	t.Run("lists valid .claude-* directories, sorted, excluding the current one", func(t *testing.T) {
		fake := newFakeFS(cat(valid(".claude"), valid(".claude-b"), valid(".claude-a"), valid(".claude-cur"),
			[]string{home + "/.claude-noprojects"}, valid(".other")), nil, nil)
		found := FindSiblingProfiles(home, home+"/.claude-cur", fake)
		if !reflect.DeepEqual(labels(found), []string{"claude", "claude-a", "claude-b"}) || found[1].Dir != home+"/.claude-a" {
			t.Errorf("got %+v", found)
		}
	})

	t.Run("also suggests the plain .claude when it is not the current one", func(t *testing.T) {
		found := FindSiblingProfiles(home, home+"/.claude-cur", newFakeFS(cat(valid(".claude"), valid(".claude-cur")), nil, nil))
		if !reflect.DeepEqual(labels(found), []string{"claude"}) {
			t.Errorf("got %+v", found)
		}
	})

	t.Run("excludes the current directory by realpath (symlink)", func(t *testing.T) {
		fake := newFakeFS(cat(valid(".claude-real"), valid(".claude-x")), map[string]string{home + "/.claude-link": home + "/.claude-real"}, nil)
		found := FindSiblingProfiles(home, home+"/.claude-link", fake)
		if !reflect.DeepEqual(labels(found), []string{"claude-x"}) {
			t.Errorf("got %+v", found)
		}
	})

	t.Run("empty when home has nothing to suggest", func(t *testing.T) {
		if found := FindSiblingProfiles(home, home+"/.claude", newFakeFS([]string{home}, nil, nil)); len(found) != 0 {
			t.Errorf("got %+v", found)
		}
	})
}

func TestCreateConfigFileIfMissing(t *testing.T) {
	t.Run("creates the file (0600) and its directory (0700) on the real fs", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "cfg", "agtop", "config.toml")
		res, err := CreateConfigFileIfMissing(OSFS{}, file, "x = 1\n")
		if err != nil || res != Created {
			t.Fatalf("got %v, %v", res, err)
		}
		fi, _ := os.Stat(file)
		di, _ := os.Stat(filepath.Dir(file))
		if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
			t.Errorf("file %v, dir %v", fi.Mode().Perm(), di.Mode().Perm())
		}
		if b, _ := os.ReadFile(file); string(b) != "x = 1\n" {
			t.Errorf("content %q", b)
		}
	})

	t.Run("never overwrites an existing file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(file, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := CreateConfigFileIfMissing(OSFS{}, file, "new")
		if err != nil || res != Exists {
			t.Fatalf("got %v, %v", res, err)
		}
		if b, _ := os.ReadFile(file); string(b) != "old" {
			t.Errorf("overwritten: %q", b)
		}
	})

	t.Run("a symlink to a regular file counts as existing", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "t")
		if err := os.WriteFile(target, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "config.toml")
		if err := os.Symlink(target, link); err != nil {
			t.Skip("no symlinks")
		}
		if res, err := CreateConfigFileIfMissing(OSFS{}, link, "new"); err != nil || res != Exists {
			t.Errorf("got %v, %v", res, err)
		}
	})

	t.Run("a directory or a broken link at the path is an error", func(t *testing.T) {
		dir := t.TempDir()
		_, err := CreateConfigFileIfMissing(OSFS{}, dir, "x")
		if err == nil || err.Error() != "the path exists but is not a regular file" {
			t.Errorf("dir: got %v", err)
		}
		link := filepath.Join(dir, "broken.toml")
		if os.Symlink(filepath.Join(dir, "missing"), link) == nil {
			_, err := CreateConfigFileIfMissing(OSFS{}, link, "x")
			if err == nil {
				t.Error("broken link: want error")
			}
		}
	})

	t.Run("an unwritable parent is an error, not a panic", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root ignores permissions")
		}
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		if _, err := CreateConfigFileIfMissing(OSFS{}, filepath.Join(dir, "sub", "c.toml"), "x"); err == nil {
			t.Error("want error")
		}
	})

	t.Run("fake fs: created then exists", func(t *testing.T) {
		fake := newFakeFS(nil, nil, nil)
		if res, err := CreateConfigFileIfMissing(fake, "/c/a.toml", "x"); err != nil || res != Created {
			t.Errorf("got %v, %v", res, err)
		}
		if res, err := CreateConfigFileIfMissing(fake, "/c/a.toml", "y"); err != nil || res != Exists {
			t.Errorf("got %v, %v", res, err)
		}
		if fake.files["/c/a.toml"] != "x" {
			t.Error("overwritten")
		}
	})
}

func TestInitConfigFile(t *testing.T) {
	run := func(fake *fakeFS, p ProfileDir) (string, string) {
		var buf bytes.Buffer
		InitConfigFile(fake, "/c/config.toml", []ProfileDir{p}, home, &buf)
		return buf.String(), fake.files["/c/config.toml"]
	}
	fake := func() *fakeFS {
		return newFakeFS([]string{home + "/.claude/projects", home + "/.claude-work/projects"}, nil, nil)
	}

	t.Run("announces the created file and writes sibling hints", func(t *testing.T) {
		msg, text := run(fake(), ProfileDir{Label: "claude", Dir: home + "/.claude", Source: SourceDefault})
		if msg != "agtop: created /c/config.toml with 1 profile; edit it to watch other directories (see --help)\n" {
			t.Errorf("msg %q", msg)
		}
		if !strings.Contains(text, "# claude-work = \"~/.claude-work\"") {
			t.Errorf("no hint in\n%s", text)
		}
	})

	t.Run("notes CLAUDE_CONFIG_DIR no longer applies", func(t *testing.T) {
		msg, _ := run(fake(), ProfileDir{Label: "claude", Dir: home + "/.claude", Source: SourceClaudeConfigDir})
		want := "agtop: created /c/config.toml with 1 profile (CLAUDE_CONFIG_DIR=" + home + "/.claude); " +
			"CLAUDE_CONFIG_DIR no longer applies, edit the file to watch other directories (see --help)\n"
		if msg != want {
			t.Errorf("msg %q", msg)
		}
	})

	t.Run("an existing file is silent", func(t *testing.T) {
		f := fake()
		f.files["/c/config.toml"] = "old"
		if msg, text := run(f, prof("claude", home+"/.claude")); msg != "" || text != "old" {
			t.Errorf("msg %q text %q", msg, text)
		}
	})

	t.Run("a failure only warns", func(t *testing.T) {
		f := fake()
		f.dirs["/c/config.toml"] = true
		msg, _ := run(f, prof("claude", home+"/.claude"))
		if msg != "agtop: warning: could not create /c/config.toml: the path exists but is not a regular file\n" {
			t.Errorf("msg %q", msg)
		}
	})
}
