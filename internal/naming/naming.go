package naming

import "strings"

const (
	maxTitleLen      = 50
	maxBranchSlugLen = 50
)

// fillerPrefixes are common conversational prefixes to strip from prompts.
var fillerPrefixes = []string{
	"please ",
	"can you ",
	"could you ",
	"i want you to ",
	"i need you to ",
	"i want to ",
	"i need to ",
	"i'd like you to ",
	"i'd like to ",
	"let's ",
	"go ahead and ",
	"hey ",
	"hi ",
	"hello ",
}

// GenerateTitle creates a short title from a user prompt using heuristics.
func GenerateTitle(prompt string) string {
	// Take first line only.
	if idx := strings.IndexByte(prompt, '\n'); idx != -1 {
		prompt = prompt[:idx]
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}

	// Strip leading slash commands (e.g. "/commit", "/review fix the bug").
	if len(prompt) > 0 && prompt[0] == '/' {
		if spaceIdx := strings.IndexByte(prompt, ' '); spaceIdx != -1 {
			prompt = strings.TrimSpace(prompt[spaceIdx+1:])
		} else {
			// Entire prompt is a slash command like "/commit" — use as-is.
			prompt = prompt[1:]
		}
	}

	// Strip filler prefixes (case-insensitive).
	for _, prefix := range fillerPrefixes {
		if strings.HasPrefix(strings.ToLower(prompt), prefix) {
			prompt = prompt[len(prefix):]
			break
		}
	}
	prompt = strings.TrimSpace(prompt)

	if prompt == "" {
		return ""
	}

	// Truncate to ~maxTitleLen chars, breaking at word boundary.
	truncated := false
	if len(prompt) > maxTitleLen {
		cut := maxTitleLen
		for cut > maxTitleLen-15 && prompt[cut] != ' ' {
			cut--
		}
		if prompt[cut] == ' ' {
			prompt = prompt[:cut]
		} else {
			prompt = prompt[:maxTitleLen]
		}
		truncated = true
	}

	if truncated {
		prompt += "…"
	}

	return prompt
}

// BranchSlug converts a session title into a git-safe branch slug suitable for
// the `claude/<slug>` rename step in the per-session-worktrees flow.
//
// Rules (mirror workspace.SanitizeBranchInput's character set but produce a
// clean slug instead of validating a partial input):
//   - lowercase
//   - any character outside [a-z0-9._] becomes '-'
//     (covers spaces, slashes, and every char git's check-ref-format disallows:
//     ~ ^ : ? * [ ] \ ` ' " and friends)
//   - collapse consecutive '-' into one
//   - strip leading '-' and '.' (git ref-format forbids both as the first char
//     of any component, and there is only one component here)
//   - strip trailing '.' and '.lock' (forbidden by git ref-format)
//   - truncate at maxBranchSlugLen, preferring the last '-' boundary so words
//     don't get cut mid-token
//   - empty result returns ""
//
// Examples:
//   - "Fix the login bug"           → "fix-the-login-bug"
//   - "Add CSV export 🚀"           → "add-csv-export"
//   - "feat/new auth (with OAuth2)" → "feat-new-auth-with-oauth2"
func BranchSlug(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}

	// Lowercase + replace disallowed chars with '-'.
	var b strings.Builder
	b.Grow(len(title))
	for _, r := range title {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	s := b.String()

	// Collapse consecutive '-'.
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}

	// Strip leading '-' / '.'.
	for len(s) > 0 && (s[0] == '-' || s[0] == '.') {
		s = s[1:]
	}
	// Strip trailing '.' (collapsed via the loop above already handles '-'s).
	for len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	s = strings.TrimRight(s, "-")
	// Strip trailing ".lock" (forbidden by git check-ref-format).
	for strings.HasSuffix(s, ".lock") {
		s = strings.TrimSuffix(s, ".lock")
		s = strings.TrimRight(s, ".-")
	}

	// Truncate at maxBranchSlugLen, preferring last '-' boundary.
	if len(s) > maxBranchSlugLen {
		cut := maxBranchSlugLen
		if i := strings.LastIndexByte(s[:cut], '-'); i > maxBranchSlugLen/2 {
			cut = i
		}
		s = s[:cut]
		s = strings.TrimRight(s, "-.")
	}

	return s
}
