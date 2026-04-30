package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/git-treeline/cli/internal/envparse"
)

func TestStripEnvQuotes(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`"hello"`, "hello"},
		{`'hello'`, "hello"},
		{`hello`, "hello"},
		{`"with \"escaped\""`, `with "escaped"`},
		{`""`, ""},
		{`''`, ""},
		{`"unterminated`, `"unterminated`},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := envparse.StripQuotes(tt.in)
			if got != tt.want {
				t.Errorf("StripQuotes(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseEnvLines(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, ".env")
	content := `# comment
PORT=3010
export APP_NAME="my app"
DATABASE_URL='postgres://localhost/test'

EMPTY=
INVALID LINE WITHOUT EQUALS
`
	_ = os.WriteFile(f, []byte(content), 0o644)

	entries, err := envparse.ParseFile(f)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"PORT":         "3010",
		"APP_NAME":     "my app",
		"DATABASE_URL": "postgres://localhost/test",
		"EMPTY":        "",
	}

	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(entries), len(want))
	}

	for _, e := range entries {
		expected, ok := want[e.Key]
		if !ok {
			t.Errorf("unexpected key %q", e.Key)
			continue
		}
		if e.Val != expected {
			t.Errorf("key %q: got %q, want %q", e.Key, e.Val, expected)
		}
	}
}

func TestParseEnvLines_FileNotFound(t *testing.T) {
	_, err := envparse.ParseFile("/nonexistent/.env")
	if err == nil {
		t.Error("expected error for missing file")
	}
}
