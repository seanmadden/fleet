---
type: changed
---
Detach from an attached session now uses tmux's native prefix-d chord (Ctrl+B D by default — whatever your tmux prefix is) instead of fleet's custom Ctrl+Q intercept. Every byte you type now passes straight through to tmux/Claude, freeing Ctrl+Q for the shell or for Claude.
