package domain

import (
	"math"
	"regexp"
	"strconv"
)

const (
	DefaultContextWindow  = 200_000
	ExtendedContextWindow = 1_000_000
)

// LearnedContextWindows are the windows learned at run time, by model id without the variant
// suffix (ModelIDBase): when some session or agent of the model used more than the table/default
// window, all others of the same model use the learned one (source inferred).
type LearnedContextWindows map[string]int

// ContextWindowSource says where a window size came from. reported: Claude Code told the
// window of this session (status line hook); model: another session of the same profile and
// model did. Both are exact. inferred: usage went past the table/default window, so it was
// promoted to 1M. The UI marks default and inferred with ~.
type ContextWindowSource string

const (
	SourceReported ContextWindowSource = "reported"
	SourceModel    ContextWindowSource = "model"
	SourceSuffix   ContextWindowSource = "suffix"
	SourceTable    ContextWindowSource = "table"
	SourceDefault  ContextWindowSource = "default"
	SourceInferred ContextWindowSource = "inferred"
)

// ReportedWindows are the windows Claude Code reported (see ContextReports): of the session
// itself and of the model. Zero means none.
type ReportedWindows struct {
	Session int
	Model   int
}

type ContextWindow struct {
	Size   int                 `json:"size"`
	Source ContextWindowSource `json:"source"`
}

// modelGenerationRE: family, major and minor generation (no minor = 0), optional date.
var modelGenerationRE = regexp.MustCompile(`^claude-(opus|sonnet|haiku|fable)-(\d+)(?:-(\d{1,2}))?(?:-\d{8})?$`)

// extendedFrom is the first generation (major, minor) of each family with 1M by default.
var extendedFrom = map[string][2]float64{
	"fable":  {5, 0},
	"opus":   {4, 6},
	"sonnet": {4, 6},
	"haiku":  {5, 0},
}

// tableWindow is the table window for an id without suffix: 1M from each family's generation,
// 200k before it; false for ids that do not follow the pattern (e.g. claude-3-5-sonnet-...).
func tableWindow(base string) (int, bool) {
	m := modelGenerationRE.FindStringSubmatch(base)
	if m == nil {
		return 0, false
	}
	from := extendedFrom[m[1]]
	major, _ := strconv.ParseFloat(m[2], 64)
	minor := 0.0
	if m[3] != "" {
		minor, _ = strconv.ParseFloat(m[3], 64)
	}
	if major > from[0] || (major == from[0] && minor >= from[1]) {
		return ExtendedContextWindow, true
	}
	return DefaultContextWindow, true
}

var (
	variantSuffixRE    = regexp.MustCompile(`\[[^\]]*\]$`)
	oneMillionSuffixRE = regexp.MustCompile(`(?i)\[1m\]$`)
)

// ModelIDBase is the id without the variant suffix (`claude-x[1m]` -> `claude-x`).
func ModelIDBase(id string) string {
	return variantSuffixRE.ReplaceAllString(id, "")
}

// ContextWindowFor returns the context window of modelID. Order: reported by the session, reported
// by another session of the same model and profile, [1m] suffix (1M), window learned by the id
// without suffix (inferred), table by model generation, and finally the default (200k).
func ContextWindowFor(modelID string, learned LearnedContextWindows, reported ReportedWindows) ContextWindow {
	if modelID == "" {
		return ContextWindow{DefaultContextWindow, SourceDefault}
	}
	if reported.Session != 0 {
		return ContextWindow{reported.Session, SourceReported}
	}
	if reported.Model != 0 {
		return ContextWindow{reported.Model, SourceModel}
	}
	base := ModelIDBase(modelID)
	if oneMillionSuffixRE.MatchString(modelID) {
		return ContextWindow{ExtendedContextWindow, SourceSuffix}
	}
	if size, ok := learned[base]; ok {
		return ContextWindow{size, SourceInferred}
	}
	if size, ok := tableWindow(base); ok {
		return ContextWindow{size, SourceTable}
	}
	return ContextWindow{DefaultContextWindow, SourceDefault}
}

// ContextPercent is the percentage (0 to 100, possibly above) of used over size; 0 if size is
// not positive or the values are not usable.
func ContextPercent(used, size float64) float64 {
	if math.IsNaN(used) || math.IsInf(used, 0) || math.IsNaN(size) || math.IsInf(size, 0) || size <= 0 || used <= 0 {
		return 0
	}
	return used / size * 100
}

// ContextUsage is a window and the context use in %.
type ContextUsage struct {
	ContextWindow
	Percent float64 `json:"percent"`
}

// ComputeContextUsage returns the window of modelID for a use of used tokens, and the %. A use
// above the table/default (or suffix) window proves the window is larger: it is promoted to 1M
// (inferred) and the % is capped at 100.
func ComputeContextUsage(modelID string, used int, learned LearnedContextWindows, reported ReportedWindows) ContextUsage {
	w := ContextWindowFor(modelID, learned, reported)
	exact := w.Source == SourceReported || w.Source == SourceModel
	if !exact && used > w.Size && w.Size < ExtendedContextWindow {
		w = ContextWindow{ExtendedContextWindow, SourceInferred}
	}
	return ContextUsage{w, math.Min(100, ContextPercent(float64(used), float64(w.Size)))}
}

// LearnContextWindow returns the window that a use of used tokens teaches about modelID (by the
// id without suffix), or false if it teaches nothing: no model, a reported window, a [1m]
// suffix, or a use within the table/default window. It ignores what was already learned.
func LearnContextWindow(modelID string, used int, reported ReportedWindows) (base string, size int, ok bool) {
	if modelID == "" {
		return "", 0, false
	}
	w := ContextWindowFor(modelID, nil, reported)
	if w.Source != SourceTable && w.Source != SourceDefault {
		return "", 0, false
	}
	if used <= w.Size || w.Size >= ExtendedContextWindow {
		return "", 0, false
	}
	return ModelIDBase(modelID), ExtendedContextWindow, true
}
