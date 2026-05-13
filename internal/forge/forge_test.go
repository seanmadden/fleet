package forge

import "testing"

func TestParseRemote(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantHost string
		wantPath string
	}{
		{"https github", "https://github.com/brizzai/fleet.git", "github.com", "brizzai/fleet"},
		{"https no .git", "https://github.com/brizzai/fleet", "github.com", "brizzai/fleet"},
		{"https trailing slash", "https://github.com/brizzai/fleet/", "github.com", "brizzai/fleet"},
		{"scp git@", "git@github.com:brizzai/fleet.git", "github.com", "brizzai/fleet"},
		{"scp no user", "github.com:brizzai/fleet.git", "github.com", "brizzai/fleet"},
		{"ssh url with port", "ssh://git@github.com:22/brizzai/fleet.git", "github.com", "brizzai/fleet"},
		{"gitlab subgroup scp", "git@gitlab.com:group/sub/repo.git", "gitlab.com", "group/sub/repo"},
		{"gitlab subgroup https", "https://gitlab.example.com/group/sub/repo.git", "gitlab.example.com", "group/sub/repo"},
		{"http", "http://gitlab.local/team/proj.git", "gitlab.local", "team/proj"},
		{"git protocol", "git://github.com/brizzai/fleet.git", "github.com", "brizzai/fleet"},
		{"empty", "", "", ""},
		{"whitespace", "  ", "", ""},
		{"garbage", "not a url", "not a url", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, path := ParseRemote(tt.url)
			if host != tt.wantHost || path != tt.wantPath {
				t.Errorf("ParseRemote(%q) = (%q, %q), want (%q, %q)", tt.url, host, path, tt.wantHost, tt.wantPath)
			}
		})
	}
}
