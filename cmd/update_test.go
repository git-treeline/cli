package cmd

import "testing"

func TestDetectInstallMethod(t *testing.T) {
	tests := []struct {
		name   string
		exe    string
		goBins []string
		want   installMethod
	}{
		{
			name: "homebrew apple silicon cellar",
			exe:  "/opt/homebrew/Cellar/git-treeline/0.56.2/bin/gtl",
			want: installHomebrew,
		},
		{
			name: "homebrew intel cellar",
			exe:  "/usr/local/Cellar/git-treeline/0.56.2/bin/gtl",
			want: installHomebrew,
		},
		{
			name: "linuxbrew cellar",
			exe:  "/home/linuxbrew/.linuxbrew/Cellar/git-treeline/0.56.2/bin/gtl",
			want: installHomebrew,
		},
		{
			name:   "gobin match",
			exe:    "/Users/dev/bin/git-treeline",
			goBins: []string{"/Users/dev/bin"},
			want:   installGoBin,
		},
		{
			name:   "default gopath bin",
			exe:    "/Users/dev/go/bin/git-treeline",
			goBins: []string{"", "/Users/dev/go/bin"},
			want:   installGoBin,
		},
		{
			name:   "cellar wins over gobin",
			exe:    "/opt/homebrew/Cellar/git-treeline/0.56.2/bin/gtl",
			goBins: []string{"/opt/homebrew/Cellar/git-treeline/0.56.2/bin"},
			want:   installHomebrew,
		},
		{
			name:   "release binary in /usr/local/bin",
			exe:    "/usr/local/bin/git-treeline",
			goBins: []string{"/Users/dev/go/bin"},
			want:   installUnknown,
		},
		{
			name: "no go bins known",
			exe:  "/Users/dev/go/bin/git-treeline",
			want: installUnknown,
		},
		{
			name:   "subdirectory of a go bin does not match",
			exe:    "/Users/dev/go/bin/nested/git-treeline",
			goBins: []string{"/Users/dev/go/bin"},
			want:   installUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectInstallMethod(tt.exe, tt.goBins); got != tt.want {
				t.Errorf("detectInstallMethod(%q, %v) = %v, want %v", tt.exe, tt.goBins, got, tt.want)
			}
		})
	}
}
