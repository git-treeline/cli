package worktree

import "testing"

func TestParseRepoSlugFromURL(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"scp-style ssh with .git", "git@github.com:acme/fitter.git", "acme/fitter"},
		{"scp-style ssh without .git", "git@github.com:acme/fitter", "acme/fitter"},
		{"https with .git", "https://github.com/acme/fitter.git", "acme/fitter"},
		{"https without .git", "https://github.com/acme/fitter", "acme/fitter"},
		{"ssh scheme", "ssh://git@github.com/acme/fitter.git", "acme/fitter"},
		{"trailing slash", "https://github.com/acme/fitter/", "acme/fitter"},
		{"gitlab subgroup takes last two", "https://gitlab.com/group/sub/fitter.git", "sub/fitter"},
		{"empty", "", ""},
		{"single segment is not a slug", "https://github.com/fitter", ""},
		{"whitespace", "  git@github.com:acme/fitter.git\n", "acme/fitter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRepoSlugFromURL(tc.url); got != tc.want {
				t.Errorf("parseRepoSlugFromURL(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}
