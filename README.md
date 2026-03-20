# Zaka

Universal CLI agent driver. Steers any AI coding agent via tmux sessions.

Named after Cheradenine Zakalwe from Iain M. Banks' *Use of Weapons* — the Culture's instrument for steering autonomous systems from the outside. The complement to [Alwe](https://github.com/mistakeknot/Alwe), which observes.

## Install

```bash
go install github.com/mistakeknot/Zaka/cmd/zaka@latest
```

Requires `tmux` at runtime.

## Usage

```bash
# Spawn an agent in a tmux session
zaka spawn --agent claude-code --workdir .

# Send a prompt to a running session
zaka steer zaka-claude-code-1710936000 "fix the auth bug"

# List active sessions
zaka list

# Kill a session
zaka kill zaka-claude-code-1710936000

# Show available agents
zaka agents
```

## Supported Agents

| Agent | Binary | Resume Support |
|-------|--------|---------------|
| Claude Code | `claude` | yes |
| Codex | `codex` | no |
| Gemini CLI | `gemini` | no |
| AMP | `amp` | no |
| Aider | `aider` | no |
| Cline | `cline` | no |
| Cursor | `cursor` | no |
| Copilot | `copilot` | no |

Adding a new agent is one line — see [AGENTS.md](AGENTS.md).

## Part of Demarch

Zaka is an L2 OS component of [Demarch](https://github.com/mistakeknot/Demarch), the autonomous software development agency platform.

## License

MIT
