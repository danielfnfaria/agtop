package setup

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const hook = "agtop statusline-hook"

func mustParse(t *testing.T, text string) *Object {
	t.Helper()
	v, err := ParseJSON(text)
	if err != nil {
		t.Fatal(err)
	}
	o, ok := v.(*Object)
	if !ok {
		t.Fatalf("%s is not an object", text)
	}
	return o
}

func compact(o *Object) string {
	if o == nil {
		return "<nil>"
	}
	return marshalJSON(o, "")
}

func TestShellQuoteAndHookCommand(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"/usr/bin/agtop", "/usr/bin/agtop"},
		{"/a b/agtop", `'/a b/agtop'`},
		{"/it's/x", `'/it'\''s/x'`},
		{"$HOME", `'$HOME'`},
	} {
		if got := ShellQuote(tt.in); got != tt.want {
			t.Errorf("ShellQuote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if got := HookCommand(true, "/x/agtop"); got != hook {
		t.Errorf("on PATH: %q", got)
	}
	if got := HookCommand(false, "/x y/agtop"); got != `'/x y/agtop' statusline-hook` {
		t.Errorf("absolute: %q", got)
	}
}

func TestWrapUnwrapCommand(t *testing.T) {
	for _, original := range []string{"my-statusline", "a; b", "a && b || c", `bash -c 'echo "{x}"'`, "cat | jq -r .model.id"} {
		t.Run("round trip: "+original, func(t *testing.T) {
			wrapped := WrapCommand(hook, original)
			want := `input=$(cat); printf '%s' "$input" | ` + hook + ` >/dev/null 2>&1; printf '%s' "$input" | { ` + original + `; }`
			if wrapped != want {
				t.Errorf("wrapped = %q", wrapped)
			}
			if got, ok := UnwrapCommand(wrapped); !ok || got != (Unwrapped{Original: original}) {
				t.Errorf("unwrapped = %+v, %v", got, ok)
			}
		})
	}
	for _, original := range []string{"a;", "a &", "a # comment", "a\nb"} {
		t.Run("newline before the brace: "+original, func(t *testing.T) {
			wrapped := WrapCommand(hook, original)
			if !strings.HasSuffix(wrapped, "{ "+original+"\n}") {
				t.Errorf("wrapped = %q", wrapped)
			}
			if got, ok := UnwrapCommand(wrapped); !ok || got.Print {
				t.Errorf("unwrapped = %+v, %v", got, ok)
			}
		})
	}
	manual := `input=$(cat); printf '%s' "$input" | node /x/dist/cli.js statusline-hook >/dev/null 2>&1; printf '%s' "$input" | bash ~/.claude/statusline.sh`
	for _, tt := range []struct {
		name, in string
		want     Unwrapped
		ok       bool
	}{
		{"hand-written form (no group)", manual, Unwrapped{Original: "bash ~/.claude/statusline.sh"}, true},
		{"pipe form", "agtop statusline-hook | my-line --x", Unwrapped{Original: "my-line --x"}, true},
		{"print form", PrintCommand(hook), Unwrapped{Print: true}, true},
		{"print form with a quoted path", `'/a b/agtop' statusline-hook --print`, Unwrapped{Print: true}, true},
		{"unknown form", "foo; statusline-hook; bar", Unwrapped{}, false},
		{"braces that are not the wrapping group stay", `input=$(cat); printf '%s' "$input" | ` + hook + ` >/dev/null 2>&1; printf '%s' "$input" | { a; } | { b; }`, Unwrapped{Original: "{ a; } | { b; }"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := UnwrapCommand(tt.in); ok != tt.ok || got != tt.want {
				t.Errorf("got %+v, %v", got, ok)
			}
		})
	}
}

func TestPlanSetup(t *testing.T) {
	t.Run("c) no statusLine: the print command, appended last", func(t *testing.T) {
		plan := PlanSetup(mustParse(t, `{"model":"x","env":{"A":"1"}}`), hook)
		if plan.Action != ActionApply {
			t.Fatalf("action %v", plan.Action)
		}
		if !slices.Equal(plan.Settings.Keys(), []string{"model", "env", "statusLine"}) {
			t.Errorf("keys %v", plan.Settings.Keys())
		}
		if got := compact(plan.After); got != `{"type":"command","command":"`+hook+` --print"}` {
			t.Errorf("after %s", got)
		}
		if plan.Before != nil || plan.Saved.Original != nil || plan.Saved.Command != hook+" --print" {
			t.Errorf("saved %+v", plan.Saved)
		}
	})

	t.Run("b) another command: wrapped, other fields and key order kept", func(t *testing.T) {
		settings := mustParse(t, `{"statusLine":{"type":"command","command":"a && b","padding":0,"refreshInterval":5},"theme":"dark"}`)
		plan := PlanSetup(settings, hook)
		if plan.Action != ActionApply {
			t.Fatalf("action %v", plan.Action)
		}
		if !slices.Equal(plan.Settings.Keys(), []string{"statusLine", "theme"}) || !slices.Equal(plan.After.Keys(), []string{"type", "command", "padding", "refreshInterval"}) {
			t.Errorf("keys %v / %v", plan.Settings.Keys(), plan.After.Keys())
		}
		if c, _ := plan.After.Get("command"); c != WrapCommand(hook, "a && b") {
			t.Errorf("command %v", c)
		}
		if compact(plan.Saved.Original) != `{"type":"command","command":"a && b","padding":0,"refreshInterval":5}` {
			t.Errorf("saved original %s", compact(plan.Saved.Original))
		}
	})

	t.Run("a) already hooked (any form): nothing to do", func(t *testing.T) {
		for _, command := range []string{hook + " --print", "input=$(cat); x | node /p statusline-hook; y"} {
			settings := mustParse(t, `{"statusLine":{"type":"command","command":`+quote(command)+`}}`)
			if plan := PlanSetup(settings, hook); plan.Action != ActionAlready {
				t.Errorf("%q: action %v", command, plan.Action)
			}
		}
	})

	t.Run("a statusLine without a command is skipped", func(t *testing.T) {
		for _, text := range []string{`{"statusLine":{"type":"command"}}`, `{"statusLine":"x"}`, `{"statusLine":{"command":"  "}}`} {
			if plan := PlanSetup(mustParse(t, text), hook); plan.Action != ActionSkip {
				t.Errorf("%s: action %v", text, plan.Action)
			}
		}
	})
}

func quote(s string) string { return marshalJSON(s, "") }

func TestPlanRemove(t *testing.T) {
	t.Run("restores exactly what setup replaced (b and c)", func(t *testing.T) {
		for _, text := range []string{
			`{"statusLine":{"type":"command","command":"a; b","padding":1},"z":1}`,
			`{"z":1}`,
		} {
			setup := PlanSetup(mustParse(t, text), hook)
			if setup.Action != ActionApply {
				t.Fatalf("setup: %v", setup.Action)
			}
			withSaved := PlanRemove(setup.Settings, &setup.Saved)
			if withSaved.Action != ActionApply || compact(withSaved.Settings) != text {
				t.Errorf("with setup.json: %s", compact(withSaved.Settings))
			}
			// Without setup.json, the known forms are unwrapped too.
			if without := PlanRemove(setup.Settings, nil); !reflect.DeepEqual(without, withSaved) {
				t.Errorf("without setup.json: %s", compact(without.Settings))
			}
		}
	})

	t.Run("nothing hooked: nothing to do; unknown form: skipped", func(t *testing.T) {
		for _, tt := range []struct {
			text string
			want Action
		}{
			{`{}`, ActionNone},
			{`{"statusLine":{"command":"x"}}`, ActionNone},
			{`{"statusLine":{"command":"a; statusline-hook; b"}}`, ActionSkip},
		} {
			if plan := PlanRemove(mustParse(t, tt.text), nil); plan.Action != tt.want {
				t.Errorf("%s: action %v", tt.text, plan.Action)
			}
		}
	})
}

func TestJSONHelpers(t *testing.T) {
	t.Run("with keeps the order, appends, removes", func(t *testing.T) {
		o := mustParse(t, `{"a":1,"b":2,"c":3}`)
		if got := o.with("b", "x", false).Keys(); !slices.Equal(got, []string{"a", "b", "c"}) {
			t.Errorf("replace: %v", got)
		}
		if got := compact(mustParse(t, `{"a":1}`).with("b", json.Number("2"), false)); got != `{"a":1,"b":2}` {
			t.Errorf("append: %s", got)
		}
		if got := compact(mustParse(t, `{"a":1,"b":2}`).with("a", nil, true)); got != `{"b":2}` {
			t.Errorf("remove: %s", got)
		}
	})

	t.Run("FormatJSON follows the indentation and final newline of the previous text", func(t *testing.T) {
		str := func(s string) *string { return &s }
		o := mustParse(t, `{"a":1}`)
		for _, tt := range []struct {
			name string
			prev *string
			want string
		}{
			{"no previous", nil, "{\n  \"a\": 1\n}\n"},
			{"four spaces, no final newline", str("{\n    \"a\": 0\n}"), "{\n    \"a\": 1\n}"},
			{"tabs, final newline", str("{\n\t\"a\": 0\n}\n"), "{\n\t\"a\": 1\n}\n"},
			{"compact previous", str("{}"), "{\n  \"a\": 1\n}"},
		} {
			if got := FormatJSON(o, tt.prev); got != tt.want {
				t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
			}
		}
	})

	t.Run("round trip keeps order, numbers and escapes; empty containers", func(t *testing.T) {
		text := "{\n  \"z\": 1.50,\n  \"a\": [],\n  \"o\": {},\n  \"s\": \"tab\\there \\\"q\\\" é </x>\",\n  \"n\": null,\n  \"l\": [1, true]\n}\n"
		want := "{\n  \"z\": 1.50,\n  \"a\": [],\n  \"o\": {},\n  \"s\": \"tab\\there \\\"q\\\" é </x>\",\n  \"n\": null,\n  \"l\": [\n    1,\n    true\n  ]\n}\n"
		if got := FormatJSON(mustParse(t, text), &text); got != want {
			t.Errorf("got %q", got)
		}
	})

	t.Run("ParseJSON rejects invalid text and trailing data", func(t *testing.T) {
		for _, text := range []string{`{ "statusLine": `, `{} x`, ``, `{"a":1,}`} {
			if _, err := ParseJSON(text); err == nil {
				t.Errorf("%q accepted", text)
			}
		}
	})

	t.Run("ParseSaved validates", func(t *testing.T) {
		for _, tt := range []struct {
			text string
			ok   bool
		}{
			{`{"version":1,"original":null,"command":"x"}`, true},
			{`{"version":1,"original":{"command":"y"},"command":"x"}`, true},
			{`{"version":2,"original":null,"command":"x"}`, false},
			{`{"version":1,"original":"x","command":"x"}`, false},
			{`{"version":1,"command":"x"}`, false},
			{`null`, false},
		} {
			v, err := ParseJSON(tt.text)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := ParseSaved(v); ok != tt.ok {
				t.Errorf("%s: %v", tt.text, ok)
			}
		}
	})

	t.Run("DiffLines", func(t *testing.T) {
		after := mustParse(t, `{"command":"x"}`)
		want := []string{"- (no statusLine)", `+ "statusLine": {`, `+   "command": "x"`, "+ }"}
		if got := DiffLines(nil, after); !slices.Equal(got, want) {
			t.Errorf("got %q", got)
		}
	})
}
