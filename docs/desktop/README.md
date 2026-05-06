# fleet — Desktop App Design Docs

Design and brainstorming docs for a native macOS app that supersedes the current TUI as fleet's primary surface. The TUI stays maintained for headless / SSH use, but the Mac app becomes the default.

These docs are **pre-implementation brainstorming**. Decisions here are working drafts, not contracts.

## Doc map

| Doc | What's in it |
|---|---|
| [vision.md](./vision.md) | What we're building, who it's for, the narrative, what we explicitly do NOT build |
| [competitive-landscape.md](./competitive-landscape.md) | Conductor deep-dive, peer scan (Crystal, Claudia, Warp, Cline, Aider), fleet's natural moats and gaps |
| [architecture.md](./architecture.md) | Process model (daemon + clients), IPC (gRPC over Unix socket), tmux client/server topology, Swift app structure, migration path |
| [ux-design.md](./ux-design.md) | V1 layout, sidebar, status grouping, ASCII mockups, keybindings, V2 multi-pane sketch, notifications |
| [design-system.md](./design-system.md) | Brand & visual system: personality, voice, color tokens, typography, spacing, shape, motion, iconography, accent rules |
| [roadmap.md](./roadmap.md) | Staged build plan: V1 TUI parity → V2 multi-pane + diff + notifs → V3+ |
| [stage-0-plan.md](./stage-0-plan.md) | Concrete PR-by-PR sequence for the daemon extraction (Stage 0). 5 PRs, ~3 weeks, TUI never breaks. |
| [stage-1-slice-1.md](./stage-1-slice-1.md) | Implementation log for V1 Mac app slice 1: skeleton + DaemonClient + sidebar + SwiftTerm pane (no mutations). Landed 2026-05-07. |
| [research-2026-05-07.md](./research-2026-05-07.md) | 10-agent competitive deep-dive synthesis: three architectural camps (terminal-attach / rebuild / server-side emulator), feature-by-feature scorecard vs Conductor / Crystal / Claudia / Cline / Claude Squad, SwiftTerm stack risks + mitigations, things to steal. Per-product raw reports in [`research-2026-05-07/`](./research-2026-05-07/). |
| [open-questions.md](./open-questions.md) | Decisions deferred: pane warm-cache, setup scripts, broadcast input, chat-wrap vs raw terminal, etc. |

## TL;DR

- **Stack**: Swift + SwiftUI + SwiftTerm, plus a long-running Go daemon (`fleet daemon`) that owns SQLite, hooks, tmux, git, and `gh`.
- **Brand**: one product called `fleet`. `fleet` opens the Mac app; `fleet --tui` opens the TUI; `fleet daemon` is the background service.
- **The bet**: keep the real Claude Code TUI inside a real terminal pane (via tmux attach + SwiftTerm). Build native chrome around it — sidebar, tabs, diff panel, notifications, dev-server panel — but never proxy or reimplement Claude's chat UI. That's where Conductor users say it "feels weird"; we sidestep that whole problem.
- **The wedge** that fleet keeps and Conductor refuses: open any local repo, no clone, no GitHub OAuth, no setup script required.

## Background research

Full competitor research lives in [`memory/desktop_research.md`](../../../.claude/projects/-Users-yuvalhayke-code-brizz-code/memory/desktop_research.md) (auto-memory, not in repo). The relevant findings are condensed into [`competitive-landscape.md`](./competitive-landscape.md).

## De-risk spike

`spikes/swiftterm-spike/` — a tiny standalone Swift app that attaches to a real fleet tmux session and renders the Claude Code TUI inside SwiftTerm. **Validated 2026-05-05**: rendering is essentially pixel-identical to Terminal.app. The biggest single risk for the Mac-app rewrite is closed.
