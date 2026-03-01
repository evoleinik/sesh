# Landscape — how sesh relates to other projects

## Comparison

| | OpenClaw | NanoClaw | Ralph Loop | sesh |
|---|---|---|---|---|
| **Problem** | AI assistant via chat | Same, but secure | Run tasks autonomously | Knowledge dies when session ends |
| **Scale** | 434k LOC, 241k stars | 3.9k LOC, 16.6k stars | Bash one-liner | Vision stage |
| **Architecture** | TypeScript gateway + plugins | TypeScript + containers | External bash loop | Python digest + ralph curation |
| **Session model** | Stateful in-process | Per-group containers | Fresh context each iteration | Extract → curate → inject |
| **Cross-session knowledge** | Manual (SOUL.md) | Manual (per-group CLAUDE.md) | Manual (AGENT.md, fix_plan.md) | Automated (stop hook → background curation) |
| **Scope** | Everything | Everything (smaller) | Task execution | Session intelligence only |

## Key insight

All three existing projects treat sessions as throwaway. Nobody mines session data for structured knowledge that feeds future sessions automatically.

- **OpenClaw/NanoClaw** solve "how do I talk to AI from my phone" — agent frameworks
- **Ralph Loop** solves "how do I run Claude autonomously for hours" — task execution
- **sesh** solves "how do I stop losing what Claude learned" — knowledge persistence

These are orthogonal. sesh could plug into any of them.

## Ralph Loop details

- Original: Geoffrey Huntley (ghuntley.com/ralph)
- Anthropic plugin: `ralph-loop@claude-plugins-official` (uses Stop Hook internally)
- Our implementation: `~/bin/ralph` (external loop, truly fresh context each iteration)

The critical distinction (per Michael Arnaldi): internal hooks reuse the same session (context accumulates and rots). External loops give genuinely fresh context — the agent re-evaluates from scratch every iteration.

## OpenClaw backstory

Created by Peter Steinberger (PSPDFKit founder). Originally Clawdbot → Moltbot (Anthropic trademark issue) → OpenClaw. Steinberger acqui-hired by OpenAI (Feb 2026). Project moved to open-source foundation. Has a conference (ClawCon) and skills marketplace (ClawHub, 5,400+ skills).

## NanoClaw backstory

Created by Gavriel Cohen as a security-first reaction to OpenClaw. Key innovation: OS-level container isolation (Docker/Apple Container) per chat group. Philosophy: "software you fork and have Claude Code customize for your exact needs."
