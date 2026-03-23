package adapter

import (
	"fmt"
	"os"
	"path/filepath"
)

// ClaudeAdapter steers Claude Code via tmux.
type ClaudeAdapter struct {
	BinaryPath string
}

func init() {
	Register(&ClaudeAdapter{})
}

func (a *ClaudeAdapter) Name() string { return "claude-code" }

func (a *ClaudeAdapter) SpawnCmd(workDir string, cfg Config) (string, []string) {
	bin := a.binary()
	args := []string{
		"--verbose",
	}
	if cfg.PermissionMode != "" {
		args = append(args, "--permission-mode", cfg.PermissionMode)
	}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	args = append(args, cfg.ExtraArgs...)
	return bin, args
}

func (a *ClaudeAdapter) ResumeCmd(sessionID string, workDir string, cfg Config) (string, []string) {
	bin := a.binary()
	args := []string{
		"--resume", sessionID,
		"--verbose",
	}
	if cfg.PermissionMode != "" {
		args = append(args, "--permission-mode", cfg.PermissionMode)
	}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	args = append(args, cfg.ExtraArgs...)
	return bin, args
}

func (a *ClaudeAdapter) FormatPrompt(prompt string) string {
	return prompt
}

func (a *ClaudeAdapter) SessionDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

func (a *ClaudeAdapter) CassConnector() string { return "claude_code" }
func (a *ClaudeAdapter) SupportsResume() bool  { return true }

func (a *ClaudeAdapter) binary() string {
	if a.BinaryPath != "" {
		return a.BinaryPath
	}
	return "claude"
}

// FindLatestSession scans Claude Code's session directory for the most
// recent JSONL file.
func FindLatestSession() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}

	base := filepath.Join(home, ".claude", "projects")
	var newest string
	var newestTime int64

	filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".jsonl" && info.ModTime().Unix() > newestTime {
			newest = path
			newestTime = info.ModTime().Unix()
		}
		return nil
	})

	if newest == "" {
		return "", fmt.Errorf("no session files found under %s", base)
	}
	return newest, nil
}
