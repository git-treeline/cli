// Package selfupdate provides the latest-release lookup, version comparison,
// and on-disk check cache behind 'gtl update' and the passive update notice.
package selfupdate

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/git-treeline/cli/internal/platform"
)

// latestReleaseURL is the unauthenticated GitHub API endpoint for the newest
// published release. Unauthenticated calls are rate-limited to 60/hour per
// IP, which the 24h check cache keeps us far under.
const latestReleaseURL = "https://api.github.com/repos/git-treeline/cli/releases/latest"

// CacheTTL is how long a cached check result is trusted before the passive
// notice path refreshes it in the background.
const CacheTTL = 24 * time.Hour

// FetchLatestVersion returns the tag name of the newest published release
// (e.g. "v0.57.0").
func FetchLatestVersion(timeout time.Duration) (string, error) {
	req, err := http.NewRequest(http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "git-treeline-cli")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub releases API returned %s", resp.Status)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("parsing release response: %w", err)
	}
	if _, ok := parseSemver(release.TagName); !ok {
		return "", fmt.Errorf("unexpected release tag %q", release.TagName)
	}
	return release.TagName, nil
}

// IsNewer reports whether latest is a strictly newer release than current.
// Returns false when either version doesn't parse (e.g. current is "dev"),
// so callers never nag on builds they can't compare.
func IsNewer(current, latest string) bool {
	c, ok := parseSemver(current)
	if !ok {
		return false
	}
	l, ok := parseSemver(latest)
	if !ok {
		return false
	}
	for i := range c {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// parseSemver parses "v1.2.3" or "1.2.3" (ignoring any -prerelease suffix)
// into major/minor/patch.
func parseSemver(s string) ([3]int, bool) {
	s = strings.TrimPrefix(s, "v")
	if base, _, found := strings.Cut(s, "-"); found {
		s = base
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// CheckState is the persisted result of the most recent release check.
type CheckState struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest_version"`
}

// Fresh reports whether the cached result is still within CacheTTL.
func (s CheckState) Fresh(now time.Time) bool {
	return s.Latest != "" && now.Sub(s.CheckedAt) < CacheTTL
}

func cacheFile() string {
	return filepath.Join(platform.ConfigDir(), "update-check.json")
}

// ReadState loads the cached check result. ok is false when there is no
// usable cache (missing, unreadable, or malformed — all treated as "never
// checked").
func ReadState() (state CheckState, ok bool) {
	data, err := os.ReadFile(cacheFile())
	if err != nil {
		return CheckState{}, false
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return CheckState{}, false
	}
	return state, true
}

// WriteState records a successful check at the given time. Best-effort:
// callers ignore failure — a missing cache just means re-checking sooner.
func WriteState(latest string, now time.Time) error {
	data, err := json.Marshal(CheckState{CheckedAt: now, Latest: latest})
	if err != nil {
		return err
	}
	return os.WriteFile(cacheFile(), data, platform.PrivateFileMode)
}
