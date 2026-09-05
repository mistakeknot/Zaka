# Zaka

Universal CLI agent driver. Steers AI coding agents via tmux, with durable Codex App Server sessions for structured questions, answers, and steering.

Named after Cheradenine Zakalwe from Iain M. Banks' *Use of Weapons* — the Culture's instrument for steering autonomous systems from the outside. The complement to [Alwe](https://github.com/mistakeknot/Alwe), which observes.

## Install

```bash
go install github.com/mistakeknot/Zaka/cmd/zaka@latest
```

Requires `tmux` for tmux transports, or `codex` for App Server transport.

See [Codex App Server transport](docs/app-server.md) for the async CLI contract,
permission policy, durable status/events, and parent integration details.

## Usage

```bash
# Spawn an agent in a tmux session
zaka spawn --agent claude-code --workdir .

# Pass repeatable backend arguments when a routed profile needs them
zaka spawn --agent codex --model gpt-6-astra --agent-arg=-c --agent-arg=model_reasoning_effort=high

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
