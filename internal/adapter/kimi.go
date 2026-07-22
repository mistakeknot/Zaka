package adapter

import (
	"os"
	"path/filepath"
)

// KimiAdapter steers the Kimi Code CLI via tmux.
//
// Kimi's TUI needs a custom submit sequence: under tmux's default
// extended-keys-format (xterm), the first Enter after typing only inserts a
// newline / acknowledges autocomplete, so a second Enter is required to
// actually submit the prompt.
type KimiAdapter struct{}

func init() {
	Register(&KimiAdapter{})
}

func (a *KimiAdapter) Name() string { return "kimi" }

func (a *KimiAdapter) SpawnCmd(workDir string, cfg Config) (string, []string) {
	var args []string
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	args = append(args, cfg.ExtraArgs...)
	return "kimi", args
}

func (a *KimiAdapter) ResumeCmd(_ string, _ string, _ Config) (string, []string) {
	return "", nil
}

func (a *KimiAdapter) FormatPrompt(prompt string) string {
	return prompt
}

// SubmitKeys returns how the prompt is submitted. Under tmux's default
// extended-keys-format (xterm), tmux delivers the Enter key in extended
// format, which Kimi's TUI misinterprets as insert-newline — the prompt is
// never submitted. Sending a raw carriage return byte (-H 0d) submits
// reliably.
func (a *KimiAdapter) SubmitKeys() []string {
	return []string{"-H", "0d"}
}

func (a *KimiAdapter) SessionDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kimi-code", "sessions")
}

func (a *KimiAdapter) CassConnector() string { return "kimi" }
func (a *KimiAdapter) SupportsResume() bool  { return false }
