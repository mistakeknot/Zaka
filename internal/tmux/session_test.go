package tmux

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// fakeTmux writes a fake tmux binary to a temp dir and prepends it to PATH.
func fakeTmux(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestListSessionsNoServer(t *testing.T) {
	fakeTmux(t, "#!/bin/sh\necho 'no server running on /private/tmp/tmux-501/default' >&2\nexit 1\n")

	sessions, err := ListSessions(context.Background())
	if err != nil {
		t.Fatalf("expected nil error when no tmux server is running, got: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected empty session list, got: %v", sessions)
	}
}

func TestListSessionsFiltersZakaPrefix(t *testing.T) {
	fakeTmux(t, "#!/bin/sh\nprintf 'zaka-kimi-123\\nother-session\\nzaka-codex-456\\n'\n")

	sessions, err := ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 || sessions[0] != "zaka-kimi-123" || sessions[1] != "zaka-codex-456" {
		t.Fatalf("unexpected sessions: %v", sessions)
	}
}

func TestListSessionsPropagatesRealErrors(t *testing.T) {
	fakeTmux(t, "#!/bin/sh\necho 'some other tmux failure' >&2\nexit 2\n")

	_, err := ListSessions(context.Background())
	if err == nil {
		t.Fatal("expected error for non-'no server' tmux failure, got nil")
	}
}
