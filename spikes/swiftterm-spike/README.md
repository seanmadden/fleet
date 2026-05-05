# SwiftTermSpike

Minimal de-risk for embedding the Claude Code TUI inside a SwiftTerm-rendered tmux pane. Throwaway code; not part of the shipped product.

## Result (2026-05-05)

**Passed.** SwiftTerm renders the Claude Code TUI essentially pixel-identical to Terminal.app: header with version/model/effort badges, pixel-art avatar, token-usage bar, status pills, box-drawing characters, colors, tmux's own bottom status bar (`ctrl+q detach`). The biggest risk for the Mac-app rewrite — "will SwiftTerm render Claude's TUI faithfully" — is conclusively answered yes.

Build issues encountered and fixed:
- Swift 6 needed `swift-tools-version:6.0` (was `5.9`).
- SPM identity collided when the directory was `spikes/swiftterm` — same identity as the SwiftTerm dependency. Renamed to `spikes/swiftterm-spike`.
- Swift 6 strict concurrency rejected closing over a top-level `let` from inside a class — moved `sessionName` into AppDelegate's init.
- Subprocess `tmux` not found because `swift run`'s child PATH excluded `/opt/homebrew/bin` — now we inherit the parent's full env and resolve `tmux` to an absolute path before spawn.

## What this proves (or disproves)

Before committing 3 weeks to the daemon refactor + Mac app build, we need confidence in four claims:

1. **Rendering fidelity** — SwiftTerm renders Claude's TUI faithfully: colors, box-drawing characters, cursor positioning, line wrapping, the spinner glyph.
2. **Performance under load** — heavy ANSI output (e.g. `cat large_file`, Claude streaming a long response, a `make build` log) doesn't stutter or drop frames.
3. **Keyboard fidelity** — typing into Claude works as expected. Arrow keys, Enter, Tab, Shift+Tab (Claude's plan-mode toggle), Ctrl+C, Ctrl+R all reach Claude.
4. **Resize correctness** — resizing the window updates tmux's pane size and Claude's TUI re-lays-out cleanly, no garbled state.

## How to run

```bash
cd spikes/swiftterm-spike

# Pick a target tmux session (must not be currently attached).
swift run SwiftTermSpike --list
swift run SwiftTermSpike fleet_brizzai_94baa34a
```

First run downloads SwiftTerm via SPM (~30s). Subsequent runs are <2s incremental builds.

## What to look for

When the window opens you should see exactly what `tmux attach -t <name>` shows in Terminal.app — the same Claude prompt, same colors, same status bar at the bottom.

Smoke checklist:

- [ ] **Static render**: tmux status bar at bottom is correct. Claude prompt visible. Cursor blinks at the prompt.
- [ ] **Type a prompt**: type "what is 2+2" + Enter. Claude responds.
- [ ] **Streaming**: ask Claude to "list 50 files in /usr/bin and describe each." Watch for stutter, dropped frames, or misaligned output during the stream.
- [ ] **Plan mode**: Shift+Tab toggles plan mode in Claude (you should see the prompt change to plan-mode style).
- [ ] **Approval prompts**: ask Claude to do something that triggers a permission prompt. The boxed `❯ 1. Yes / 2. No` menu should render with sharp box-drawing chars, no fallback ASCII.
- [ ] **Resize**: drag the window edges. Tmux pane size should update; Claude's UI should re-flow without artifacts.
- [ ] **Ctrl+Q**: should send Ctrl+Q to tmux (which has nothing bound by default — won't detach). This proves keystrokes pass through cleanly.
- [ ] **Heavy log**: in Claude session, run `seq 1 100000 | cat`. Window should stay responsive; no beach-ball.

If anything looks wrong, take a screenshot and note which checklist item failed.

## Known limitations of the spike (not blockers for the real app)

- **Window quits → tmux session detaches but stays alive**. SwiftTerm sends SIGHUP on dealloc, which tmux interprets as detach. Verify by running `tmux ls` after closing — your session should still be there.
- **No clean detach UX** — closing the window is the only exit. The real app intercepts a key chord (Ctrl+Q or similar) and detaches programmatically, leaving the rest of the app running.
- **No theme integration** — uses default SwiftTerm colors. The real app will set the palette from settings.
- **No multi-session, no sidebar, no toolbars.** This is not the app; it's one window proving one technical claim.

## What we do if the spike fails

- **Rendering looks wrong**: check SwiftTerm version, file an issue if needed. Worst case: pivot to a different terminal widget (iTerm2's open-source terminal core, or build on Apple's own Swift-Term-Adjacent libraries).
- **Performance is bad**: profile with Instruments. SwiftTerm uses a CALayer-backed renderer; if it can't keep up with Claude's output, something's wrong with our config (font, anti-aliasing, line spacing). If it's a fundamental SwiftTerm ceiling, reconsider the stack — Tauri+xterm.js becomes a plausible plan B since xterm.js with WebGL has been benchmarked at handling >10MB/s.
- **Keyboard fails**: usually a `optionAsMetaKey` or `allowMouseReporting` config issue, or our PTY env doesn't have the right `TERM`. Tweak before declaring failure.

## After this passes

Delete the spike or keep it as a reference. Either way, do NOT let it accumulate features — it's a tracer bullet, not a starting skeleton. The real Mac app starts fresh in `macapp/` after Stage 0 lands.
