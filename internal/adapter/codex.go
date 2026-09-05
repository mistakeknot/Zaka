package adapter

// CodexAdapter steers Codex CLI via tmux.
type CodexAdapter struct{}

func init() {
	Register(&CodexAdapter{})
}

func (a *CodexAdapter) Name() string { return "codex" }

func (a *CodexAdapter) SpawnCmd(workDir string, cfg Config) (string, []string) {
	args := []string{}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	if cfg.PermissionMode != "" {
		args = append(args, "--sandbox", cfg.PermissionMode)
	}
	args = append(args, cfg.ExtraArgs...)
	return "codex", args
}

func (a *CodexAdapter) ResumeCmd(_ string, _ string, _ Config) (string, []string) {
	return "", nil
}

func (a *CodexAdapter) FormatPrompt(prompt string) string {
	return prompt
}

func (a *CodexAdapter) SessionDir() string    { return "" }
func (a *CodexAdapter) CassConnector() string { return "codex" }
func (a *CodexAdapter) SupportsResume() bool  { return false }
