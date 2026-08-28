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

func TestParseVersionOutput(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string
	}{
		{"normal output", "git-treeline v0.57.0\n", "v0.57.0"},
		{"dev build", "git-treeline dev\n", "dev"},
		{"empty output", "", ""},
		{"unexpected binary name", "other-tool v1.0.0\n", ""},
		{"extra fields", "git-treeline v0.57.0 extra\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseVersionOutput(tt.out); got != tt.want {
				t.Errorf("parseVersionOutput(%q) = %q, want %q", tt.out, got, tt.want)
			}
		})
	}
}

func TestShouldCheckForUpdate(t *testing.T) {
	tests := []struct {
		name        string
		rootCmd     string
		cliVersion  string
		suppressEnv string
		isTTY       bool
		want        bool
	}{
		{"normal command on release build", "status", "v0.56.2", "", true, true},
		{"suppressed by env", "status", "v0.56.2", "1", true, false},
		{"not a tty", "status", "v0.56.2", "", false, false},
		{"dev build", "status", "dev", "", true, false},
		{"empty version", "status", "", "", true, false},
		{"update command itself", "update", "v0.56.2", "", true, false},
		{"version command", "version", "v0.56.2", "", true, false},
		{"completion handler", "__complete", "v0.56.2", "", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldCheckForUpdate(tt.rootCmd, tt.cliVersion, tt.suppressEnv, tt.isTTY)
			if got != tt.want {
				t.Errorf("shouldCheckForUpdate(%q, %q, %q, %v) = %v, want %v",
					tt.rootCmd, tt.cliVersion, tt.suppressEnv, tt.isTTY, got, tt.want)
			}
		})
	}
}
