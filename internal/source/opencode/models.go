package opencode

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/domain"
)

// opencode caches the model catalog it downloads in <cache dir>/opencode/models.json:
// `<providerID>.models.<modelID>` holds the display name and `limit.context`, the context
// window. The catalog is read once per source (it is the same file for every profile); without
// it, or without the model, the viewer falls back to its own estimate.

const (
	// EnvXDGCacheHome selects the cache directory, like opencode does.
	EnvXDGCacheHome = "XDG_CACHE_HOME"
	// maxModelsBytes caps the catalog read.
	maxModelsBytes = 32 << 20
	minContext     = 1_000
	maxContext     = 100_000_000
)

// ModelsPath is $XDG_CACHE_HOME/opencode/models.json, else <homedir>/.cache/opencode/models.json.
func ModelsPath(env config.Env, homedir string) string {
	base := filepath.Join(homedir, ".cache")
	if xdg := strings.TrimSpace(env[EnvXDGCacheHome]); xdg != "" && filepath.IsAbs(xdg) {
		base = xdg
	}
	return filepath.Join(base, "opencode", "models.json")
}

// modelEntry is what the catalog says about one model.
type modelEntry struct {
	Name    string
	Context int
}

// catalog is the lazily loaded models.json.
type catalog struct {
	path string
	once sync.Once
	// byKey is "<providerID>/<modelID>" to entry; nil when the file is missing or invalid.
	byKey map[string]modelEntry
}

func newCatalog(path string) *catalog { return &catalog{path: path} }

type catalogModel struct {
	Name  string `json:"name"`
	Limit struct {
		Context float64 `json:"context"`
	} `json:"limit"`
}

type catalogProvider struct {
	Models map[string]catalogModel `json:"models"`
}

func (c *catalog) load() {
	f, err := os.Open(c.path)
	if err != nil {
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxModelsBytes {
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, maxModelsBytes))
	if err != nil {
		return
	}
	var providers map[string]catalogProvider
	if json.Unmarshal(data, &providers) != nil {
		return
	}
	byKey := map[string]modelEntry{}
	for providerID, p := range providers {
		for modelID, m := range p.Models {
			e := modelEntry{Name: domain.Sanitize(m.Name, nameMax)}
			if ctx := m.Limit.Context; ctx >= minContext && ctx <= maxContext && ctx == float64(int(ctx)) {
				e.Context = int(ctx)
			}
			byKey[providerID+"/"+modelID] = e
		}
	}
	c.byKey = byKey
}

// lookup is the catalog entry of a model; false when unknown.
func (c *catalog) lookup(providerID, modelID string) (modelEntry, bool) {
	if c == nil || modelID == "" {
		return modelEntry{}, false
	}
	c.once.Do(c.load)
	e, ok := c.byKey[providerID+"/"+modelID]
	return e, ok
}
