package ui

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/ingest/watcher"
	"github.com/danielfnfaria/agtop/internal/store"
	"github.com/danielfnfaria/agtop/internal/ui/style"
)

func TestStorePanicEndsRunWithTheStack(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".claude")
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	in, inW := io.Pipe()
	defer func() { _ = inW.Close() }()
	var out, errOut bytes.Buffer
	o := Options{
		Profiles:   []config.ProfileDir{{Label: "claude", Dir: dir, Source: config.SourceConfig}},
		Appearance: style.DefaultAppearance(),
		Input:      in, Output: &out, Stderr: &errOut,
		storeDeps: store.Deps{
			NewWatcher: func(watcher.Target) (watcher.Watcher, error) { panic("watcher boom") },
		},
	}
	type result struct {
		code int
		err  error
	}
	res := make(chan result, 1)
	go func() {
		code, err := Run(context.Background(), o)
		res <- result{code, err}
	}()
	var r result
	select {
	case r = <-res:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not end after the store panicked")
	}
	if r.code != 1 || r.err != nil {
		t.Errorf("Run = %d, %v; want 1, nil", r.code, r.err)
	}
	got := errOut.String()
	for _, want := range []string{
		"agtop: 1 message held during full screen:\nagtop: the store stopped after a panic: watcher boom\n",
		"agtop: panic: watcher boom\n", "goroutine ", "store.(*Store)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "held during full screen") > strings.Index(got, "agtop: panic:") {
		t.Error("the panic is printed before the held messages")
	}
}
