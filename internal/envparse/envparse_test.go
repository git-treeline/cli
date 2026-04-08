package envparse

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStripQuotes(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{`"hello world"`, "hello world"},
		{`'single quoted'`, "single quoted"},
		{`noquotes`, "noquotes"},
		{`""`, ""},
		{`''`, ""},
		{`"with \"escape\""`, `with "escape"`},
		{`  "padded"  `, "padded"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := StripQuotes(tt.in)
			if got != tt.want {
				t.Errorf("StripQuotes(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, ".env")
	content := `# comment
PORT=3000
DATABASE_URL="postgres://localhost/mydb"
export API_KEY='secret'
EMPTY=

INVALID LINE WITHOUT EQUALS
`
	_ = os.WriteFile(f, []byte(content), 0o644)

	entries, err := ParseFile(f)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"PORT":         "3000",
		"DATABASE_URL": "postgres://localhost/mydb",
		"API_KEY":      "secret",
		"EMPTY":        "",
	}
	if len(entries) != len(want) {
		t.Fatalf("expected %d entries, got %d", len(want), len(entries))
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

func TestParseFile_NotFound(t *testing.T) {
	_, err := ParseFile("/nonexistent/.env")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestParseFile_Empty(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, ".env")
	_ = os.WriteFile(f, []byte(""), 0o644)

	entries, err := ParseFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestParseFile_ExportPrefix(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, ".env")
	_ = os.WriteFile(f, []byte("export FOO=bar\nexport BAZ=\"qux\"\n"), 0o644)

	entries, err := ParseFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Key != "FOO" || entries[0].Val != "bar" {
		t.Errorf("entry 0: got %+v", entries[0])
	}
	if entries[1].Key != "BAZ" || entries[1].Val != "qux" {
		t.Errorf("entry 1: got %+v", entries[1])
	}
}
