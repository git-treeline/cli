package selfupdate

import (
	"testing"
	"time"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		name            string
		current, latest string
		want            bool
	}{
		{"newer patch", "v0.56.2", "v0.57.0", true},
		{"newer major", "v0.57.0", "v1.0.0", true},
		{"same version", "v0.57.0", "v0.57.0", false},
		{"current ahead of latest", "v0.58.0", "v0.57.0", false},
		{"dev build never outdated", "dev", "v0.57.0", false},
		{"empty current", "", "v0.57.0", false},
		{"empty latest", "v0.56.2", "", false},
		{"unparseable latest", "v0.56.2", "nightly", false},
		{"no v prefix", "0.56.2", "0.57.0", true},
		{"prerelease suffix ignored", "v0.56.2", "v0.57.0-rc.1", true},
		{"numeric not lexicographic", "v0.9.0", "v0.10.0", true},
		{"two-part version rejected", "v0.56", "v0.57.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNewer(tt.current, tt.latest); got != tt.want {
				t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

func TestCheckStateFresh(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		state CheckState
		want  bool
	}{
		{"just checked", CheckState{CheckedAt: now.Add(-time.Minute), Latest: "v0.57.0"}, true},
		{"within ttl", CheckState{CheckedAt: now.Add(-23 * time.Hour), Latest: "v0.57.0"}, true},
		{"past ttl", CheckState{CheckedAt: now.Add(-25 * time.Hour), Latest: "v0.57.0"}, false},
		{"empty latest never fresh", CheckState{CheckedAt: now.Add(-time.Minute)}, false},
		{"zero state", CheckState{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.state.Fresh(now); got != tt.want {
				t.Errorf("Fresh() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStateRoundTrip(t *testing.T) {
	t.Setenv("GTL_HOME", t.TempDir())

	if _, ok := ReadState(); ok {
		t.Fatal("ReadState() ok = true with no cache file")
	}

	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	if err := WriteState("v0.57.0", now); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	state, ok := ReadState()
	if !ok {
		t.Fatal("ReadState() ok = false after WriteState")
	}
	if state.Latest != "v0.57.0" || !state.CheckedAt.Equal(now) {
		t.Errorf("ReadState() = %+v, want Latest v0.57.0 at %v", state, now)
	}
}
