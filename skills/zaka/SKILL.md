---
name: zaka
description: Use when you need to drive an interactive CLI coding agent (Claude Code, Codex, Kimi, Gemini, AMP, Aider) programmatically — spawn it in a tmux session, send it prompts mid-run, read its output, or resume it later. For one-shot headless dispatch use dispatch.sh instead; for observing session history use the alwe skill.
---

# Zaka — steer interactive agent sessions

Zaka spawns any AI coding agent in a tmux session and steers it via send-keys. The complement to Alwe, which observes.

## When to use Zaka vs. alternatives

- **Zaka**: multi-turn control of a *live interactive* agent — you need to see its TUI, answer its prompts, steer mid-task, or keep a long-running session alive.
- **dispatch.sh** (`os/Clavain/scripts/dispatch.sh`): one-shot headless dispatch (`codex exec`, `kimi -p`) — fire-and-forget tasks where you only need the final output.
- **intermux MCP**: read-only peeking at tmux panes already running.
- **Alwe**: historical session search, not live control.

## CLI

```bash
zaka agents                                    # list registered agent adapters
zaka spawn --agent claude-code --workdir .     # start a session (prints session name)
zaka spawn --agent kimi --workdir /path/to/project
zaka steer <session-name> "fix the auth bug"   # send a prompt to a running session
zaka list                                      # active sessions
zaka kill <session-name>
```

## Adapter model

Each adapter knows how to spawn, resume, and format prompts for one CLI agent (`claude-code`, `codex`, `kimi`, `gemini`, `amp`, `aider`, `cline`, `cursor`, `copilot`). Run `zaka agents` for the current registry — it shows each adapter's cass connector and whether resume is supported.

## Operational notes

- Requires `tmux` at runtime.
- Spawn returns a session name like `zaka-kimi-1710936000` — capture it for later `steer`/`kill`.
- Zaka sessions are real tmux sessions: you can also `tmux capture-pane -t <name> -p` to read output directly.
- Repo: `/Users/sma/projects/Sylveste/os/Zaka` (`go build ./cmd/zaka`, `go test ./...`).
