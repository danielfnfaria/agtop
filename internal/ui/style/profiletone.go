package style

// ProfileTone returns the color of the profile at index, cycling through palette (the dark
// theme's when nil).
func ProfileTone(index int, palette []Color) Color {
	if palette == nil {
		palette = DarkTheme.Profiles
	}
	n := len(palette)
	if n == 0 {
		return "magenta"
	}
	return palette[((index%n)+n)%n]
}

// ProfileToneMap maps each label to its color, in the order of labels.
func ProfileToneMap(labels []string, palette []Color) map[string]Color {
	m := make(map[string]Color, len(labels))
	for i, l := range labels {
		m[l] = ProfileTone(i, palette)
	}
	return m
}
