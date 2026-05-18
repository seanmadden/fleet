package naming

import "testing"

func TestGenerateTitle(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"single word", "refactor", "refactor"},
		{"preserves casing", "Fix the Login Bug", "Fix the Login Bug"},
		{"strips filler please", "please fix the tests", "fix the tests"},
		{"strips filler can you", "Can you add a button", "add a button"},
		{"strips filler lets", "let's refactor the auth module", "refactor the auth module"},
		{"case insensitive filler", "PLEASE update the README", "update the README"},
		{"slash command with text", "/commit fix the build", "fix the build"},
		{"slash command alone", "/commit", "commit"},
		{"multiline takes first line", "fix the bug\nsecond line\nthird line", "fix the bug"},
		{
			"truncation at word boundary",
			"implement the new authentication system with OAuth2 support and refresh tokens for all API endpoints",
			"implement the new authentication system with\u2026",
		},
		{
			"truncation preserves original casing",
			"Add comprehensive error handling for the database connection pooling layer with retries",
			"Add comprehensive error handling for the database\u2026",
		},
		{"unicode content", "修复登录问题", "修复登录问题"},
		{"filler then slash", "please /review the code", "/review the code"},
		{"only filler", "please ", "please"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateTitle(tt.input)
			if got != tt.want {
				t.Errorf("GenerateTitle(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBranchSlug(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"single word", "refactor", "refactor"},
		{"lowercases", "Fix the Login Bug", "fix-the-login-bug"},
		{"strips spaces and symbols", "Add CSV export 🚀", "add-csv-export"},
		{"strips slashes", "feat/new auth", "feat-new-auth"},
		{"strips git-forbidden chars", "feat~bad^stuff:here", "feat-bad-stuff-here"},
		{"strips parens", "feat/new auth (with OAuth2)", "feat-new-auth-with-oauth2"},
		{"collapses consecutive dashes", "feat--bar___baz", "feat-bar___baz"},
		{"strips leading dash", "-bad", "bad"},
		{"strips leading dot", ".bad", "bad"},
		{"strips trailing dot", "good.", "good"},
		{"strips trailing dash", "good-", "good"},
		{"strips .lock suffix", "feature.lock", "feature"},
		{"keeps underscore", "fix_bug_123", "fix_bug_123"},
		{"keeps dots interior", "v1.2.3", "v1.2.3"},
		{"truncates at boundary", "implement the new authentication system with OAuth2 support and refresh tokens for all API endpoints", "implement-the-new-authentication-system-with"},
		{"unicode replaced", "修复登录问题", ""},
		{"mixed unicode plus ascii", "fix 登录 bug", "fix-bug"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BranchSlug(tt.input)
			if got != tt.want {
				t.Errorf("BranchSlug(%q) = %q, want %q", tt.input, got, tt.want)
			}
			// Idempotence.
			if twice := BranchSlug(got); twice != got {
				t.Errorf("BranchSlug not idempotent: %q -> %q -> %q", tt.input, got, twice)
			}
		})
	}
}
