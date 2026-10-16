package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/ui"
)

func TestRenderFailed(t *testing.T) {
	var stderr bytes.Buffer
	if code := renderFailed(&stderr, errors.New("boom")); code != 1 {
		t.Errorf("code %d", code)
	}
	if got := stderr.String(); got != "agtop: error while rendering: boom\n" {
		t.Errorf("stderr %q", got)
	}
}

func TestRenderOnceRecoversAPanic(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := renderOnce(func(ui.Options) string { panic("bad frame") }, ui.Options{}, &stderr, &stdout)
	if code != 1 || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "agtop: error while rendering: bad frame\n") {
		t.Errorf("code %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

func TestStyleMode(t *testing.T) {
	for _, tt := range []struct {
		name string
		env  config.Env
		tty  bool
		want ui.StyleMode
	}{
		{"terminal", config.Env{}, true, ui.StyleFull},
		{"terminal, TERM=dumb", config.Env{"TERM": "dumb"}, true, ui.StyleNone},
		{"terminal, TERM=dumb, NO_COLOR", config.Env{"TERM": "dumb", "NO_COLOR": "1"}, true, ui.StyleNone},
		{"pipe", config.Env{}, false, ui.StyleNone},
		{"pipe, NO_COLOR", config.Env{"NO_COLOR": "1"}, false, ui.StyleNone},
		{"pipe, FORCE_COLOR=0", config.Env{"FORCE_COLOR": "0"}, false, ui.StyleNone},
		{"pipe, FORCE_COLOR=1", config.Env{"FORCE_COLOR": "1"}, false, ui.StyleFull},
		{"pipe, empty FORCE_COLOR forces", config.Env{"FORCE_COLOR": ""}, false, ui.StyleFull},
		{"terminal, FORCE_COLOR=0", config.Env{"FORCE_COLOR": "0"}, true, ui.StyleAttributes},
		{"terminal, FORCE_COLOR=false", config.Env{"FORCE_COLOR": "false"}, true, ui.StyleAttributes},
		{"terminal, NO_COLOR", config.Env{"NO_COLOR": "1"}, true, ui.StyleAttributes},
		{"NO_COLOR wins over FORCE_COLOR", config.Env{"NO_COLOR": "1", "FORCE_COLOR": "1"}, true, ui.StyleAttributes},
		{"empty NO_COLOR is ignored", config.Env{"NO_COLOR": "", "FORCE_COLOR": "1"}, false, ui.StyleFull},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := Command{NoColor: tt.env["NO_COLOR"] != ""}
			if got := styleMode(cmd, System{Env: tt.env, StdoutTTY: tt.tty}); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
