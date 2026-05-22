---
type: added
---
Mobile-friendly web UI embedded in the fleet TUI process. Enable in `~/.config/fleet/config.json` with `{"web": {"enabled": true, "addr": "0.0.0.0:8765"}}` and fleet exposes a phone-friendly browser interface for listing sessions, viewing pane output, sending keys, quick-approving, restarting, deleting, and creating sessions. Auth is a bearer token (auto-generated on first run if `token` is empty and written back to the config); when listening on a non-loopback address the token must be present. SSE drives live session-list updates; pane content polls at ~1s while viewing. No build step — assets are embedded.
