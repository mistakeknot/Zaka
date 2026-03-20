# Zaka: Vision and Philosophy

## What Zaka Is

Zaka is a universal CLI agent driver. It spawns any AI coding agent in a tmux session and steers it via send-keys — giving orchestrators like Skaffen a uniform interface to heterogeneous agents without vendor lock-in or stripped-down subprocess modes.

The name comes from Cheradenine Zakalwe in Iain M. Banks' *Use of Weapons* — the Culture's instrument for steering autonomous systems from the outside. Zaka is the acting half; [Alwe](https://github.com/mistakeknot/Alwe) is the observing half. Together they form Zakalwe.

## Core Conviction

**Interactive mode is the real agent.** Every CLI agent has a stripped-down subprocess mode (`claude -p`, `codex exec`) that loses capabilities — plugins don't load, session history is discarded, hooks don't fire. The interactive mode *is* the agent. Zaka steers the real agent, not a lobotomized proxy.

**tmux is the universal agent bus.** Every CLI agent speaks terminal. That's the lowest common denominator — no SDK, no API, no protocol negotiation. If it runs in a terminal, Zaka can steer it. This will remain true even as agents evolve, because terminals are the most stable interface in computing.

**Adapters, not abstractions.** Zaka doesn't abstract away agent differences — it adapts to them. Each agent gets an adapter that knows its CLI flags, permission modes, and session management. The adapter interface is thin (6 methods) because the real complexity lives in the agents themselves.

## Architecture

```
Orchestrator (Skaffen, CI, script, human)
  │
  ▼
Zaka adapter registry
  │
  ├── ClaudeAdapter   → tmux → claude (full plugin runtime)
  ├── CodexAdapter    → tmux → codex (sandbox bypass)
  ├── GenericAdapter   → tmux → gemini / amp / aider / ...
  │
  └── Session manager (spawn, send-keys, capture-pane, kill)
```

### Three-tier observation (via Alwe)

Zaka steers. [Alwe](https://github.com/mistakeknot/Alwe) observes. The tiers:

1. **Structured (CASS):** Agents with CASS connectors get parsed JSONL events — tool calls, text deltas, usage.
2. **Screen scrape:** Agents without CASS connectors fall back to tmux capture-pane + regex extraction.
3. **Raw:** Any CLI tool — raw text output, orchestrator interprets via its own LLM.

## Design Bets

1. **tmux outlives agent SDKs.** Agent vendors will change their APIs, add/remove features, break compatibility. tmux send-keys will work the same way it has for 30 years.

2. **The adapter pattern scales.** Adding a new agent is one line (`Register(NewGeneric("name", "binary", "connector"))`). Specialized adapters only for agents that need custom spawn/resume logic.

3. **Steering and observation are separate concerns.** Zaka has no CASS dependency. Alwe has no tmux dependency. They compose through the orchestrator, but either can be used independently.

4. **Session persistence matters.** Claude Code's `--resume` flag lets Zaka maintain conversation context across steering commands. As more agents add session persistence, the adapters will expose it.

## Non-Goals

- **Not a workflow engine.** Zaka spawns and steers. It doesn't decide *what* to steer toward — that's the orchestrator's job (Skaffen's OODARC loop, a CI script, a human).
- **Not an agent abstraction layer.** Zaka doesn't unify agent APIs into a common interface. Each agent keeps its own capabilities and limitations. The adapter just handles spawn/resume/format.
- **Not an MCP server.** The MCP server for observation lives in Alwe, not Zaka. Zaka is pure control plane.

## Relationship to Skaffen

Skaffen is the sovereign agent runtime — it runs the OODARC loop (Observe, Orient, Decide, Act, Reflect, Compound). Zaka is the Act implementation: when Skaffen decides to delegate work to another agent, it uses Zaka to spawn and steer that agent. Skaffen's `internal/provider/tmuxagent/` is a thin bridge that adapts Zaka's types to Skaffen's provider interface.

## Relationship to Alwe

Zaka and Alwe are two halves of one operation, named from the same character. They share no code and have no dependency on each other. They compose through the orchestrator:

```
Skaffen OODARC loop:
  Decide  → which agent, which task
  Act     → Zaka spawns agent, sends prompt
  Observe → Alwe reads session output via CASS
  Reflect → Skaffen evaluates results
```
