package config

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/danielfnfaria/agtop/internal/ui/style"
	"github.com/pelletier/go-toml/v2"
)

// FileData is what the config file defines.
type FileData struct {
	// Profiles is nil when [profiles] is missing or empty.
	Profiles []ProfileDir
	// Appearance holds [theme], [glyphs] and [layout]; nil when none is in the file.
	Appearance *style.AppearanceConfig
}

var sections = []string{"profiles", "theme", "glyphs", "layout"}

// ParseFile interprets the file text with a strict schema: only [profiles] (label -> path),
// [theme], [glyphs] and [layout]; anything else is an error naming file (used only for
// messages) and key.
func ParseFile(text, file, homedir string) (FileData, error) {
	var doc map[string]any
	if err := toml.Unmarshal([]byte(text), &doc); err != nil {
		var de *toml.DecodeError
		if errors.As(err, &de) {
			return FileData{}, errorf("%s: %s", file, de.String())
		}
		return FileData{}, errorf("%s: %v", file, err)
	}
	order := orderKeys(text)

	for _, key := range order.keys("", doc) {
		if !contains(sections, key) {
			names := make([]string, len(sections))
			for i, s := range sections {
				names[i] = "[" + s + "]"
			}
			return FileData{}, errorf("%s: unknown key \"%s\" (expected %s)", file, key, strings.Join(names, ", "))
		}
	}

	profilesTable, err := table(doc["profiles"], "profiles", file)
	if err != nil {
		return FileData{}, err
	}
	var profiles []ProfileDir
	for _, label := range order.keys("profiles", profilesTable) {
		value := profilesTable[label]
		if strings.TrimSpace(label) == "" {
			return FileData{}, errorf("%s: profiles has an empty label", file)
		}
		s, ok := value.(string)
		if !ok {
			return FileData{}, errorf("%s: profiles.%s must be a string path", file, label)
		}
		raw := strings.TrimSpace(s)
		if raw == "" {
			return FileData{}, errorf("%s: profiles.%s is an empty path", file, label)
		}
		dir := ExpandHome(raw, homedir)
		if !filepath.IsAbs(dir) {
			return FileData{}, errorf("%s: profiles.%s = \"%s\" is not absolute (use an absolute path or ~)", file, label, raw)
		}
		profiles = append(profiles, ProfileDir{Label: label, Dir: filepath.Clean(dir), Source: SourceConfig})
	}

	appearance, err := parseAppearance(doc, order, file)
	if err != nil {
		return FileData{}, err
	}
	return FileData{Profiles: profiles, Appearance: appearance}, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// table returns v as a table; nil (no error) when v is absent. Arrays, scalars and TOML dates
// are not tables.
func table(v any, name, file string) (map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errorf("%s: [%s] must be a table", file, name)
	}
	return m, nil
}

// LoadOptions are the inputs of LoadFile.
type LoadOptions struct {
	Env     Env
	Homedir string
	// Cwd is the base for a relative --config; defaults to Homedir.
	Cwd string
}

// Loaded is the result of LoadFile.
type Loaded struct {
	FileData
	// File is the file read; empty when there is no file at the default path.
	File string
}

// LoadFile reads the config file. explicitPath (--config) is resolved against the cwd and must
// exist; without it the default path is used and a missing file is not an error.
func LoadFile(explicitPath *string, fsys FS, opts LoadOptions) (Loaded, error) {
	var file string
	if explicitPath != nil {
		cwd := opts.Cwd
		if cwd == "" {
			cwd = opts.Homedir
		}
		file = resolvePath(cwd, ExpandHome(*explicitPath, opts.Homedir))
	} else {
		file = DefaultConfigPath(opts.Env, opts.Homedir)
	}
	text, found, err := fsys.ReadFile(file)
	if err != nil {
		return Loaded{}, errorf("cannot read config file %s: %v", file, err)
	}
	if !found {
		if explicitPath != nil {
			return Loaded{}, errorf("config file not found: %s", file)
		}
		return Loaded{}, nil
	}
	data, err := ParseFile(text, file, opts.Homedir)
	if err != nil {
		return Loaded{}, err
	}
	return Loaded{FileData: data, File: file}, nil
}
