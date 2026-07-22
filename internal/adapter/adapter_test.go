package adapter

import (
	"testing"
)

func TestAdapterRegistry(t *testing.T) {
	names := List()
	if len(names) == 0 {
		t.Fatal("no adapters registered")
	}

	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	for _, want := range []string{"claude-code", "codex", "gemini", "amp", "aider", "kimi"} {
		if !found[want] {
			t.Errorf("adapter %q not registered; have %v", want, names)
		}
	}
}

func TestClaudeAdapterSpawnCmd(t *testing.T) {
	a := Get("claude-code")
	if a == nil {
		t.Fatal("claude-code adapter not registered")
	}

	bin, args := a.SpawnCmd("/tmp/project", Config{
		Model:          "opus",
		PermissionMode: "bypassPermissions",
	})
	if bin != "claude" {
		t.Errorf("binary = %q, want claude", bin)
	}

	hasFlag := func(flag string) bool {
		for _, a := range args {
			if a == flag {
				return true
			}
		}
		return false
	}

	if !hasFlag("--verbose") {
		t.Error("missing --verbose flag")
	}
	if !hasFlag("bypassPermissions") {
		t.Error("missing permission mode")
	}
	if !hasFlag("opus") {
		t.Error("missing model")
	}
}

func TestClaudeAdapterResumeCmd(t *testing.T) {
	a := Get("claude-code")
	if !a.SupportsResume() {
		t.Fatal("claude-code should support resume")
	}

	_, args := a.ResumeCmd("session-123", "/tmp/project", Config{})
	hasResume := false
	for i, a := range args {
		if a == "--resume" && i+1 < len(args) && args[i+1] == "session-123" {
			hasResume = true
		}
	}
	if !hasResume {
		t.Error("missing --resume session-123")
	}
}

func TestCodexAdapterNoResume(t *testing.T) {
	a := Get("codex")
	if a == nil {
		t.Fatal("codex adapter not registered")
	}
	if a.SupportsResume() {
		t.Error("codex should not support resume")
	}
	if a.CassConnector() != "codex" {
		t.Errorf("connector = %q, want codex", a.CassConnector())
	}
}

func TestKimiAdapter(t *testing.T) {
	a := Get("kimi")
	if a == nil {
		t.Fatal("kimi adapter not registered")
	}
	if a.CassConnector() != "kimi" {
		t.Errorf("connector = %q, want kimi", a.CassConnector())
	}
	if a.SupportsResume() {
		t.Error("kimi should not support resume")
	}

	bin, args := a.SpawnCmd("/tmp", Config{Model: "k2"})
	if bin != "kimi" {
		t.Errorf("binary = %q, want kimi", bin)
	}
	if len(args) != 2 || args[0] != "--model" || args[1] != "k2" {
		t.Errorf("args = %v, want [--model k2]", args)
	}

	// Kimi's TUI misinterprets tmux's extended-format Enter as newline, so
	// the adapter submits via a raw carriage return byte instead.
	ks, ok := a.(KeySubmitter)
	if !ok {
		t.Fatal("kimi adapter should implement KeySubmitter")
	}
	keys := ks.SubmitKeys()
	if len(keys) != 2 || keys[0] != "-H" || keys[1] != "0d" {
		t.Errorf("SubmitKeys = %v, want [-H 0d]", keys)
	}
}

func TestGenericAdapter(t *testing.T) {
	a := NewGeneric("test-agent", "/usr/bin/test-agent", "test_connector", "--flag1")
	if a.Name() != "test-agent" {
		t.Errorf("name = %q", a.Name())
	}
	if a.CassConnector() != "test_connector" {
		t.Errorf("connector = %q", a.CassConnector())
	}
	if a.SupportsResume() {
		t.Error("generic adapter should not support resume")
	}

	bin, args := a.SpawnCmd("/tmp", Config{ExtraArgs: []string{"--extra"}})
	if bin != "/usr/bin/test-agent" {
		t.Errorf("binary = %q", bin)
	}
	if len(args) != 2 || args[0] != "--flag1" || args[1] != "--extra" {
		t.Errorf("args = %v", args)
	}
}
