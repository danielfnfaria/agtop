package domain

// ProviderID identifies a coding agent whose sessions agtop shows.
type ProviderID string

// Provider describes a provider. The Symbol is one plain Unicode character of width 1 (text
// presentation, no emoji), shown before titles and profile labels; Color is a #rrggbb hex.
type Provider struct {
	ID          ProviderID
	DisplayName string
	Symbol      string
	Color       string
}

// ClaudeCode is the provider of the sessions and profiles agtop reads today.
const ClaudeCode ProviderID = "claude-code"

// Opencode is the provider of the sessions of opencode (https://opencode.ai).
const Opencode ProviderID = "opencode"

// Providers is the registry.
var Providers = map[ProviderID]Provider{
	ClaudeCode: {
		ID:          ClaudeCode,
		DisplayName: "Claude Code",
		Symbol:      "✳", // EIGHT SPOKED ASTERISK
		Color:       "#D97757",
	},
	Opencode: {
		ID:          Opencode,
		DisplayName: "opencode",
		Symbol:      "◆", // BLACK DIAMOND
		Color:       "#4F8FF7",
	},
}

// ProviderInfo returns the registered provider of id. An id without an entry still gets a
// usable provider: its id as the name and a neutral symbol and color.
func ProviderInfo(id ProviderID) Provider {
	if p, ok := Providers[id]; ok {
		return p
	}
	return Provider{ID: id, DisplayName: string(id), Symbol: "?", Color: "#808080"}
}
