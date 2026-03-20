// Package tmux manages tmux sessions for steering CLI agents.
package tmux

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mistakeknot/Zaka/internal/adapter"
)

// Session manages a single tmux session running a CLI agent.
type Session struct {
	Name    string // tmux session name
	Adapter adapter.AgentAdapter
	WorkDir string
}

// Spawn creates a new tmux session and starts the agent inside it.
func Spawn(ctx context.Context, a adapter.AgentAdapter, workDir string, cfg adapter.Config) (*Session, error) {
	name := cfg.SessionName
	if name == "" {
		name = fmt.Sprintf("zaka-%s-%d", a.Name(), time.Now().UnixMilli())
	}

	bin, args := a.SpawnCmd(workDir, cfg)
	if bin == "" {
		return nil, fmt.Errorf("adapter %s returned empty spawn command", a.Name())
	}

	tmuxArgs := []string{
		"new-session", "-d",
		"-s", name,
		"-c", workDir,
		bin,
	}
	tmuxArgs = append(tmuxArgs, args...)

	cmd := exec.CommandContext(ctx, "tmux", tmuxArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("tmux new-session: %w: %s", err, string(out))
	}

	return &Session{
		Name:    name,
		Adapter: a,
		WorkDir: workDir,
	}, nil
}

// Resume resumes an existing agent session in a new tmux session.
func Resume(ctx context.Context, a adapter.AgentAdapter, sessionID string, workDir string, cfg adapter.Config) (*Session, error) {
	if !a.SupportsResume() {
		return nil, fmt.Errorf("adapter %s does not support session resume", a.Name())
	}

	name := cfg.SessionName
	if name == "" {
		name = fmt.Sprintf("zaka-%s-%d", a.Name(), time.Now().UnixMilli())
	}

	bin, args := a.ResumeCmd(sessionID, workDir, cfg)
	if bin == "" {
		return nil, fmt.Errorf("adapter %s returned empty resume command", a.Name())
	}

	tmuxArgs := []string{
		"new-session", "-d",
		"-s", name,
		"-c", workDir,
		bin,
	}
	tmuxArgs = append(tmuxArgs, args...)

	cmd := exec.CommandContext(ctx, "tmux", tmuxArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("tmux new-session (resume): %w: %s", err, string(out))
	}

	return &Session{
		Name:    name,
		Adapter: a,
		WorkDir: workDir,
	}, nil
}

// SendPrompt sends a prompt to the agent via tmux send-keys.
func (s *Session) SendPrompt(ctx context.Context, prompt string) error {
	formatted := s.Adapter.FormatPrompt(prompt)

	cmd := exec.CommandContext(ctx, "tmux", "send-keys", "-t", s.Name, formatted, "Enter")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys: %w: %s", err, string(out))
	}
	return nil
}

// CapturePane captures the current visible pane content.
func (s *Session) CapturePane(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", "capture-pane", "-t", s.Name, "-p", "-S", "-200")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w", err)
	}
	return string(out), nil
}

// IsAlive checks whether the tmux session still exists.
func (s *Session) IsAlive(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "tmux", "has-session", "-t", s.Name)
	return cmd.Run() == nil
}

// Kill destroys the tmux session.
func (s *Session) Kill(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "tmux", "kill-session", "-t", s.Name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux kill-session: %w: %s", err, string(out))
	}
	return nil
}

// ListSessions returns all tmux sessions matching the zaka- prefix.
func ListSessions(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}")
	out, err := cmd.Output()
	if err != nil {
		if strings.Contains(err.Error(), "no server running") {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux list-sessions: %w", err)
	}

	var sessions []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, "zaka-") {
			sessions = append(sessions, line)
		}
	}
	return sessions, nil
}
