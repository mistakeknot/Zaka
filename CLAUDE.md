# Zaka

Universal CLI agent driver. Steers any AI coding agent (Claude Code, Codex, Gemini, AMP, Aider, etc.) via tmux sessions, observes output through CASS.

Named after Cheradenine Zakalwe from Iain M. Banks' *Use of Weapons* — the Culture's instrument for steering autonomous systems from the outside.

## Quick Reference

- **Build:** `go build ./cmd/zaka && go build ./cmd/zaka-sidecar`
- **Test:** `go test ./... -count=1`
- **Vet:** `go vet ./...`
- **CLI:** `./zaka spawn --agent claude-code --workdir .`
- **MCP sidecar:** `./zaka-sidecar` (stdio MCP server backed by CASS)

## Structure

```
cmd/zaka/              CLI entry point (spawn, steer, list, kill, agents)
cmd/zaka-sidecar/      MCP sidecar server (CASS observations over stdio)
internal/
  adapter/             AgentAdapter interface + per-agent implementations
  observer/            CASS observer (real-time tail + query)
  tmux/                tmux session lifecycle (spawn, send, capture, kill)
  mcpsidecar/          MCP server exposing CASS tools
```

## Git

Zaka has its own git repo at `os/Zaka/`. Commit from here, not the monorepo root.

## Beads

Uses the Demarch monorepo beads tracker at `/home/mk/projects/Demarch/.beads/` (prefix `Demarch-`).
