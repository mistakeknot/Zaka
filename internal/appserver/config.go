// Package appserver owns durable Codex App Server connections. It never
// substitutes models, retries rejected operations, or grants server approvals.
package appserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	WorkDir        string          `json:"workdir"`
	Model          string          `json:"model"`
	Sandbox        string          `json:"sandbox"`
	ApprovalPolicy string          `json:"approval_policy"`
	AgentArgs      []string        `json:"agent_args,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
}

// routing recognizes only overrides whose meaning this transport can preserve.
// In particular it cannot accept profiles or TOML overrides of security policy.
func (c Config) routing() (args []string, effort, tier string, err error) {
	// Codex 0.153.3 otherwise withholds request_user_input in execution mode.
	// Scope this experimental capability to this transport, never the base profile.
	args = []string{"app-server", "--enable", "default_mode_request_user_input"}
	for i := 0; i < len(c.AgentArgs); i += 2 {
		if c.AgentArgs[i] != "-c" || i+1 == len(c.AgentArgs) {
			return nil, "", "", fmt.Errorf("app-server agent args must be -c key=value pairs")
		}
		k, v, ok := strings.Cut(c.AgentArgs[i+1], "=")
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		if !ok {
			return nil, "", "", fmt.Errorf("invalid config override")
		}
		switch k {
		case "model_reasoning_effort":
			if v == "" {
				return nil, "", "", fmt.Errorf("reasoning effort must be nonempty")
			}
			effort = v
		case "service_tier":
			if strings.EqualFold(v, "standard") {
				v = "default"
			}
			if v != "default" && v != "priority" && v != "flex" {
				return nil, "", "", fmt.Errorf("unsupported service tier %q", v)
			}
			tier = v
		default:
			return nil, "", "", fmt.Errorf("unsupported app-server config override %q (only model_reasoning_effort and service_tier)", k)
		}
		cliValue := v
		if strings.ContainsAny(v, " \t\n\r\"'\\=[]{}#") {
			cliValue = strconv.Quote(v)
		}
		args = append(args, "-c", k+"="+cliValue)
	}
	return
}

// Verify the effective thread settings before exposing a ready session. Managed
// policy rejection or model/effort substitution is an error, never a fallback.
func (c Config) verifyThread(result json.RawMessage) error {
	var p struct {
		Model    string `json:"model"`
		Cwd      string `json:"cwd"`
		Approval string `json:"approvalPolicy"`
		Reviewer string `json:"approvalsReviewer"`
		Sandbox  struct {
			Type string `json:"type"`
		} `json:"sandbox"`
		Effort string `json:"reasoningEffort"`
		Tier   string `json:"serviceTier"`
	}
	if err := json.Unmarshal(result, &p); err != nil {
		return fmt.Errorf("thread/start effective settings: %w", err)
	}
	_, effort, tier, _ := c.routing()
	sandbox := "workspaceWrite"
	if c.Sandbox == "read-only" {
		sandbox = "readOnly"
	}
	requestedDir, err := filepath.EvalSymlinks(c.WorkDir)
	if err != nil {
		return err
	}
	effectiveDir, err := filepath.EvalSymlinks(p.Cwd)
	if err != nil {
		return fmt.Errorf("effective cwd: %w", err)
	}
	if p.Model != c.Model || p.Approval != c.ApprovalPolicy || p.Reviewer != "user" || p.Sandbox.Type != sandbox || requestedDir != effectiveDir || (effort != "" && p.Effort != effort) || (tier != "" && p.Tier != tier) {
		return fmt.Errorf("thread/start effective model, policy, cwd, effort, or service tier differs from request; inspect event log")
	}
	return nil
}

func (c Config) CommandArgs() ([]string, error) {
	if c.Model == "" {
		return nil, fmt.Errorf("app-server requires --model")
	}
	if c.Sandbox != "read-only" && c.Sandbox != "workspace-write" {
		return nil, fmt.Errorf("app-server requires --sandbox read-only or workspace-write")
	}
	switch c.ApprovalPolicy {
	case "untrusted", "on-request", "never":
	default:
		return nil, fmt.Errorf("approval-policy must be untrusted, on-request, or never")
	}
	if len(c.Metadata) > 0 && !json.Valid(c.Metadata) {
		return nil, fmt.Errorf("metadata-json must be valid JSON")
	}
	info, err := os.Stat(c.WorkDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workdir is not a directory")
	}
	args, _, _, err := c.routing()
	return args, err
}

func (c Config) threadParams() map[string]any {
	_, effort, tier, _ := c.routing()
	p := map[string]any{"model": c.Model, "cwd": c.WorkDir, "sandbox": c.Sandbox, "approvalPolicy": c.ApprovalPolicy, "approvalsReviewer": "user", "ephemeral": false}
	if effort != "" {
		p["config"] = map[string]any{"model_reasoning_effort": effort}
	}
	if tier != "" {
		p["serviceTier"] = tier
	}
	return p
}

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("state directory must be a private directory (0700): %s", path)
	}
	return nil
}

func writeJSON(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
