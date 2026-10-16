package hook

import (
	"os/exec"
	"strings"
	"testing"
)

// The hook runs on every status line refresh: it must not link the UI or the rendering libraries.
func TestDoesNotDependOnTheUI(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, banned := range []string{"/internal/ui", "bubbletea", "lipgloss", "charmbracelet", "runewidth"} {
			if strings.Contains(dep, banned) {
				t.Errorf("hook depends on %s", dep)
			}
		}
	}
}
