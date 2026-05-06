# fleet — Brand & Design System v1

> Source of truth for fleet's visual identity. Locked 2026-05-06. Dark-only V1.
> Companion to [ux-design.md](./ux-design.md) (layout & flows) and [vision.md](./vision.md) (product narrative).

## Identity

**fleet**, by **brizzai**. A native macOS app for running many Claude Code sessions in parallel. The terminal stays a real terminal; fleet adds the chrome around it — sidebar, multi-pane, diff viewer, native notifications, contextual git actions.

**Personality**: opinionated & sharp — Linear's discipline with Vercel/Cursor's edge. Pro-tool calm, never decorative.

**One-liner**: *iTerm2-class shell that hosts Claude Code, with the chrome people show off in screenshots.*

## Voice

Terse and technical. Tool talks like a pro tool.

- *"Open PR." "Restart session." "No sessions yet."*
- No exclamations. No first-person ("Let's get started" → no).
- No emoji in product copy.
- Microcopy never apologizes and never celebrates. It states.

## Color tokens

### Brand accent (pink)

Pink is the single signature color. Used in ~5% of pixels.

| Token | Hex | Use |
|---|---|---|
| `accent` | `#F472B6` | Primary buttons, selected sidebar row rail, focus rings, slot/key chips |
| `accent/hover` | `#F9A8D4` | Lighter pink for button hover |
| `accent/pressed` | `#DB2777` | Darker pink for active/pressed |
| `accent/soft` | `#F472B6` @ 12% alpha | Tinted background fills (selected row bg, hover tint) |

### Neutrals (cool near-black)

| Token | Hex | Use |
|---|---|---|
| `bg/canvas` | `#0A0A0B` | App background, terminal pane bg |
| `bg/sidebar` | `#111113` | Sidebar background (one step elevated) |
| `bg/elevated` | `#17171A` | Dialogs, popovers, command palette |
| `bg/hover` | `#1C1C20` | Row hover |
| `border/hairline` | `#1F1F22` | 1px panel separators |
| `border/strong` | `#2A2A2F` | Input borders, button outlines |
| `text/primary` | `#F4F4F5` | Body text, sidebar titles |
| `text/secondary` | `#A1A1AA` | Meta text, branch names |
| `text/dim` | `#52525B` | Disabled, hints, low-priority chrome |

### Status palette (punchy, dual-encoded with glyphs)

Status colors are load-bearing — they convey "needs you" / "done" / "broken." Always paired with a unique glyph so colorblind users don't lose info.

| State | Glyph | Hex | Use |
|---|---|---|---|
| Running | ● | `#22C55E` | Active session |
| Waiting | ◐ | `#F59E0B` | **Highest-attention — needs you** |
| Error | ✕ | `#EF4444` | Errored session |
| Finished | ● | `#10B981` | Soft green, slightly cooler than running |
| Idle / Starting | ○ | `#71717A` | Neutral gray |
| Merged (PR) | ⇡ | `#A78BFA` | PR merged badge |

### PR badge palette

| State | Color | Glyph |
|---|---|---|
| Approved + CI passed | `#22C55E` | ✓ |
| Pending | `#F59E0B` | (none) |
| CI failed | `#EF4444` | ✕ |
| Changes requested / unresolved threads | `#EF4444` | ↩ |
| Merged | `#A78BFA` | ⇡ |
| Closed | hidden | — |

## Typography

| Role | Font | Weights |
|---|---|---|
| UI | **Inter** | 400, 500, 600 |
| Mono / terminal / code / diff | **JetBrains Mono** | 400, 500 |
| Wordmark | Inter, lowercase, 600, `-0.01em` tracking | — |

### Type scale (Linear-dense)

| Token | Size / line-height | Use |
|---|---|---|
| `text/xs` | 11 / 14 | Slot badges, key chips, table meta |
| `text/sm` | 12 / 16 | Sidebar rows, secondary toolbar text |
| `text/base` | 13 / 18 | Body, buttons, primary toolbar |
| `text/md` | 15 / 20 | Section headers, dialog titles |
| `text/lg` | 18 / 24 | Empty-state headlines (sparingly) |

## Spacing & density

- **Grid**: 4px. Everything snaps to multiples of 4.
- **Sidebar row**: 26px tall, 12px horizontal padding, 8px gap between glyph and label
- **Toolbar**: 36px tall
- **Buttons**: 28px tall (default), 32px tall (primary), 12px horizontal padding
- **Inputs**: 28px tall, 10px horizontal padding
- **Dialog padding**: 20px
- **Min window**: 900 × 600

Density target: a power user with ~30 sessions across 4 repos can see all of them in the sidebar without scrolling on a 14" laptop.

## Shape

| Surface | Radius |
|---|---|
| Buttons / inputs / chips / badges | 6px |
| Cards | 8px |
| Dialogs / sheets | 10px |
| Terminal pane | 0px (bleeds to container) |

**Borders**: 1px hairlines using `border/hairline`.
**Shadows**: none on chrome. Dialogs get native macOS shadows only.
**Surfaces**: solid + hairlines. No vibrancy/translucency, no glassy gradients.

## Motion

- **Style**: subtle & fast. Ease-out, no spring, no bounce.
- **Durations**: 120–180ms for hovers, sidebar selection, panel opens.
- **Reduced motion**: cut all transitions to 0ms when system setting is enabled.
- Motion supports recognition; it never decorates.

## Iconography

- **Set**: **Lucide**, stroke 1.5px, 16px default size.
- **Status icons**: keep the geometric glyphs above (● ◐ ○ ✕ ⇡ ✓ ↩) — *not* Lucide replacements. They're load-bearing and carry TUI muscle memory.
- **No emoji** anywhere in chrome.
- **No "AI sparkle" / star icons** — the product *is* AI; we don't advertise it.

## Wordmark

- *fleet* — Inter, lowercase, weight 600, tracking `-0.01em`.
- No custom mark in V1. Wordmark only.
- Dock icon: defer until V1 is mocked; placeholder uses the wordmark glyph "f" on `bg/sidebar` with `accent` underline.

## Accent usage rules

Pink shows up in four places, each with a different intensity:

| Surface | Intensity | Notes |
|---|---|---|
| Primary button | Full `#F472B6` fill | One primary per screen, max. |
| Selected sidebar row | 2px left rail + `accent/soft` row bg | Always visible — anchors "where am I" |
| Focus ring on inputs | 2px `#F472B6` outline | Brightest moment — only when focused |
| Slot bindings `[1]` & key chips | `#F472B6` @ 70% opacity | Quieter so 30 rows of slot badges don't overwhelm |

**Rule of thumb**: if pink ever competes with the amber "Waiting" status for attention, the amber wins — pink steps down (drop opacity, shrink the rail).

**Pink never lands on a destructive action.** Delete is red, no pink confirmation.

## Hard nos

- No custom Claude chat UI inside the app — the terminal pane shows the *real* Claude TUI, never a reimplementation.
- No emoji in product copy.
- No gradient buttons, glassy aurora backgrounds, hero illustrations.
- No "AI sparkle" / star icons.
- No marketing-sized type inside the app.
- No first-person microcopy ("Let's get started" / "We'll guide you").
- No light mode in V1.
- No drop shadows on chrome (hairlines only).
- No vibrancy/translucency.
- No spring or bounce animations.
- No mobile/responsive considerations — Mac desktop only.

## Load-bearing tests

When mocking screens, three things must be true:

1. The **Waiting** state still screams louder than the brand pink.
2. The sidebar is legible at 30 rows without feeling busy.
3. The terminal pane feels like Terminal.app — not like a styled box.

If any of those breaks, the mock is wrong, not the system.
