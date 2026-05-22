package pathx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpand_TildePrefix(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("UserHomeDir failed: %v", err)
	}
	got := Expand("~/foo")
	want := filepath.Join(home, "foo")
	if got != want {
		t.Errorf("Expand(~/foo) = %q, want %q", got, want)
	}
}

func TestExpand_BareTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("UserHomeDir failed: %v", err)
	}
	if got := Expand("~"); got != home {
		t.Errorf("Expand(~) = %q, want %q", got, home)
	}
}

func TestExpand_AbsolutePath(t *testing.T) {
	if got := Expand("/etc"); got != "/etc" {
		t.Errorf("Expand(/etc) = %q, want /etc", got)
	}
}

func TestExpand_RelativePath(t *testing.T) {
	got := Expand("foo/bar")
	if !filepath.IsAbs(got) {
		t.Errorf("Expand(foo/bar) = %q, want absolute path", got)
	}
	if !strings.HasSuffix(got, "foo/bar") {
		t.Errorf("Expand(foo/bar) = %q, want suffix foo/bar", got)
	}
}
