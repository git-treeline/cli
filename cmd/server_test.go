package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/config"
)

// captureStderr runs fn and returns everything written to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w

	fn()

	_ = w.Close()
	os.Stderr = orig

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func TestWarnPortWiring_ViteWithoutPort(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "vite.config.ts"), []byte("export default {}"), 0644)

	out := captureStderr(t, func() { warnPortWiring("npm run dev", dir) })

	if !strings.Contains(out, "Port wiring") {
		t.Errorf("expected port wiring warning for Vite, got: %q", out)
	}
	if !strings.Contains(out, "Vite ignores") {
		t.Errorf("expected Vite-specific hint text, got: %q", out)
	}
	if !strings.Contains(out, "gtl doctor") {
		t.Errorf("expected pointer to gtl doctor, got: %q", out)
	}
}

func TestWarnPortWiring_NextJSWithoutPort(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "next.config.js"), []byte("module.exports = {}"), 0644)

	out := captureStderr(t, func() { warnPortWiring("npm run dev", dir) })

	if !strings.Contains(out, "Port wiring") {
		t.Errorf("expected port wiring warning for Next.js, got: %q", out)
	}
	if !strings.Contains(out, "Next.js reads PORT") {
		t.Errorf("expected Next.js-specific hint text, got: %q", out)
	}
}

func TestWarnPortWiring_DjangoWithoutPort(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "manage.py"), []byte("#!/usr/bin/env python"), 0644)

	out := captureStderr(t, func() { warnPortWiring("python manage.py runserver", dir) })

	if !strings.Contains(out, "Port wiring") {
		t.Errorf("expected port wiring warning for Django, got: %q", out)
	}
}

func TestWarnPortWiring_ViteWithPort(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "vite.config.ts"), []byte("export default {}"), 0644)

	out := captureStderr(t, func() { warnPortWiring("npx vite --port {port}", dir) })

	if out != "" {
		t.Errorf("expected no warning when {port} is present, got: %q", out)
	}
}

func TestWarnPortWiring_GoProject(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)

	out := captureStderr(t, func() { warnPortWiring("go run .", dir) })

	if out != "" {
		t.Errorf("expected no warning for Go (no PortHint), got: %q", out)
	}
}

func TestWarnPortWiring_RailsProject(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "Gemfile"), []byte("source 'https://rubygems.org'"), 0644)
	_ = os.MkdirAll(filepath.Join(dir, "config"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "config", "application.rb"), []byte(""), 0644)

	out := captureStderr(t, func() { warnPortWiring("bin/rails server", dir) })

	if out != "" {
		t.Errorf("expected no warning for Rails (reads PORT natively), got: %q", out)
	}
}

func TestWarnPortWiring_UnknownFramework(t *testing.T) {
	dir := t.TempDir()

	out := captureStderr(t, func() { warnPortWiring("./my-server", dir) })

	if out != "" {
		t.Errorf("expected no warning for unknown framework, got: %q", out)
	}
}

func TestWarnStaleCommand_PrintsWhenDifferent(t *testing.T) {
	out := captureStderr(t, func() {
		warnStaleCommand(os.Stderr, "npm run dev --port 3000", "npm run dev --port 4000")
	})
	if !strings.Contains(out, "commands.start has changed") {
		t.Errorf("expected stale command warning, got: %q", out)
	}
	if !strings.Contains(out, "gtl start") {
		t.Errorf("expected hint to run gtl start, got: %q", out)
	}
}

func TestWarnStaleCommand_SilentWhenSame(t *testing.T) {
	out := captureStderr(t, func() {
		warnStaleCommand(os.Stderr, "npm run dev --port 3000", "npm run dev --port 3000")
	})
	if out != "" {
		t.Errorf("expected no output when commands match, got: %q", out)
	}
}

// --- Start hook tests ---

func TestResolveStartHooks_NoFlag(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: test\n"), 0o644)
	pc := config.LoadProjectConfig(dir)

	hooks, err := resolveStartHooks(pc, "")
	if err != nil {
		t.Fatal(err)
	}
	if hooks != nil {
		t.Errorf("expected nil, got %v", hooks)
	}
}

func TestResolveStartHooks_ValidHook(t *testing.T) {
	dir := t.TempDir()
	yml := "project: test\nhooks:\n  oauth:\n    pre_start: echo go\n    post_stop: echo done\n"
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(yml), 0o644)
	pc := config.LoadProjectConfig(dir)

	hooks, err := resolveStartHooks(pc, "oauth")
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 1 || hooks[0].Name != "oauth" {
		t.Errorf("expected [oauth], got %v", hooks)
	}
}

func TestResolveStartHooks_MultipleHooks(t *testing.T) {
	dir := t.TempDir()
	yml := "project: test\nhooks:\n  oauth:\n    pre_start: echo a\n  workers:\n    pre_start: echo b\n"
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(yml), 0o644)
	pc := config.LoadProjectConfig(dir)

	hooks, err := resolveStartHooks(pc, "oauth,workers")
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 2 {
		t.Errorf("expected 2 hooks, got %d", len(hooks))
	}
}

func TestResolveStartHooks_UnknownHook(t *testing.T) {
	dir := t.TempDir()
	yml := "project: test\nhooks:\n  oauth:\n    pre_start: echo go\n"
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(yml), 0o644)
	pc := config.LoadProjectConfig(dir)

	_, err := resolveStartHooks(pc, "bogus")
	if err == nil {
		t.Fatal("expected error for unknown hook")
	}
	if !strings.Contains(err.Error(), "Unknown hook") {
		t.Errorf("expected 'Unknown hook' in error, got: %s", err)
	}
}

func TestResolveStartHooks_NoHooksDefined(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: test\n"), 0o644)
	pc := config.LoadProjectConfig(dir)

	_, err := resolveStartHooks(pc, "oauth")
	if err == nil {
		t.Fatal("expected error when no hooks defined")
	}
	if !strings.Contains(err.Error(), "No hooks defined") {
		t.Errorf("expected 'No hooks defined' in error, got: %s", err)
	}
}

func TestRunPreStartHooks_Success(t *testing.T) {
	dir := t.TempDir()
	hooks := []startHookEntry{
		{Name: "test", Hook: config.StartHook{PreStart: []string{"echo hello > " + filepath.Join(dir, "out.txt")}}},
	}
	if err := runPreStartHooks(hooks, 3000, dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatal("hook command didn't write file")
	}
	if !strings.Contains(string(data), "hello") {
		t.Errorf("unexpected content: %s", data)
	}
}

func TestRunPreStartHooks_Interpolation(t *testing.T) {
	dir := t.TempDir()
	hooks := []startHookEntry{
		{Name: "test", Hook: config.StartHook{PreStart: []string{"echo {port} > " + filepath.Join(dir, "port.txt")}}},
	}
	if err := runPreStartHooks(hooks, 4567, dir); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "port.txt"))
	if !strings.Contains(string(data), "4567") {
		t.Errorf("expected interpolated port 4567, got: %s", data)
	}
}

func TestRunPreStartHooks_FailureAborts(t *testing.T) {
	hooks := []startHookEntry{
		{Name: "fail", Hook: config.StartHook{PreStart: []string{"exit 1"}}},
	}
	err := runPreStartHooks(hooks, 3000, os.TempDir())
	if err == nil {
		t.Fatal("expected error on hook failure")
	}
	if !strings.Contains(err.Error(), "fail") {
		t.Errorf("expected hook name in error, got: %s", err)
	}
}

func TestRunPreStartHooks_SkipsEmptyPreStart(t *testing.T) {
	hooks := []startHookEntry{
		{Name: "cleanup-only", Hook: config.StartHook{PostStop: []string{"echo done"}}},
	}
	if err := runPreStartHooks(hooks, 3000, os.TempDir()); err != nil {
		t.Fatalf("expected no error for empty pre_start, got: %s", err)
	}
}

func TestRunPreStartHooks_MultipleCommands(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "order.txt")
	hooks := []startHookEntry{
		{Name: "multi", Hook: config.StartHook{PreStart: []string{
			"echo first >> " + outFile,
			"echo second >> " + outFile,
		}}},
	}
	if err := runPreStartHooks(hooks, 3000, dir); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(outFile)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || strings.TrimSpace(lines[0]) != "first" || strings.TrimSpace(lines[1]) != "second" {
		t.Errorf("expected [first, second] in order, got: %q", string(data))
	}
}

func TestResolveStartHooks_AutoCollected(t *testing.T) {
	dir := t.TempDir()
	yml := "project: test\nhooks:\n  prepare:\n    auto: true\n    pre_start: echo auto\n  oauth:\n    pre_start: echo manual\n"
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(yml), 0o644)
	pc := config.LoadProjectConfig(dir)

	// No --with flag: only auto hooks returned
	hooks, err := resolveStartHooks(pc, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 1 || hooks[0].Name != "prepare" {
		t.Errorf("expected [prepare], got %v", hooks)
	}
}

func TestResolveStartHooks_AutoPlusWith(t *testing.T) {
	dir := t.TempDir()
	yml := "project: test\nhooks:\n  prepare:\n    auto: true\n    pre_start: echo auto\n  oauth:\n    pre_start: echo manual\n"
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(yml), 0o644)
	pc := config.LoadProjectConfig(dir)

	hooks, err := resolveStartHooks(pc, "oauth")
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 2 {
		t.Errorf("expected 2 hooks (auto + with), got %d", len(hooks))
	}
	names := make([]string, len(hooks))
	for i, h := range hooks {
		names[i] = h.Name
	}
	if !contains(names, "prepare") || !contains(names, "oauth") {
		t.Errorf("expected prepare and oauth, got %v", names)
	}
}

func TestResolveStartHooks_AutoDedup(t *testing.T) {
	dir := t.TempDir()
	yml := "project: test\nhooks:\n  prepare:\n    auto: true\n    pre_start: echo auto\n"
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(yml), 0o644)
	pc := config.LoadProjectConfig(dir)

	hooks, err := resolveStartHooks(pc, "prepare")
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 1 {
		t.Errorf("expected 1 hook (deduped), got %d", len(hooks))
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func TestHooksStateRoundTrip(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "test.sock")
	hooks := []startHookEntry{
		{Name: "oauth"},
		{Name: "workers"},
	}
	writeHooksState(sockPath, hooks)

	names := readHooksState(sockPath)
	if len(names) != 2 || names[0] != "oauth" || names[1] != "workers" {
		t.Errorf("expected [oauth, workers], got %v", names)
	}

	cleanHooksState(sockPath)
	if names := readHooksState(sockPath); names != nil {
		t.Errorf("expected nil after clean, got %v", names)
	}
}

func TestHooksStatePath(t *testing.T) {
	got := hooksStatePath("/tmp/gtl-abc123.sock")
	if got != "/tmp/gtl-abc123.hooks" {
		t.Errorf("expected /tmp/gtl-abc123.hooks, got %s", got)
	}
}

func TestRunPostStopHooks_ReverseOrder(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "order.txt")

	yml := "project: test\nhooks:\n  first:\n    post_stop: echo first >> " + outFile + "\n  second:\n    post_stop: echo second >> " + outFile + "\n"
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(yml), 0o644)
	pc := config.LoadProjectConfig(dir)

	sockPath := filepath.Join(dir, "test.sock")
	hooks := []startHookEntry{
		{Name: "first"},
		{Name: "second"},
	}
	writeHooksState(sockPath, hooks)

	runPostStopHooks(sockPath, pc, 3000, dir)

	data, _ := os.ReadFile(outFile)
	lines := strings.TrimSpace(string(data))
	parts := strings.Split(lines, "\n")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) != "second" || strings.TrimSpace(parts[1]) != "first" {
		t.Errorf("expected reverse order [second, first], got: %q", lines)
	}

	if names := readHooksState(sockPath); names != nil {
		t.Errorf("expected state file cleaned, got %v", names)
	}
}

func TestRunPostStopHooks_ArrayCommands(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "multi.txt")

	yml := `project: test
hooks:
  cleanup:
    post_stop:
      - echo a >> ` + outFile + `
      - echo b >> ` + outFile + `
`
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(yml), 0o644)
	pc := config.LoadProjectConfig(dir)

	sockPath := filepath.Join(dir, "test.sock")
	writeHooksState(sockPath, []startHookEntry{{Name: "cleanup"}})

	runPostStopHooks(sockPath, pc, 3000, dir)

	data, _ := os.ReadFile(outFile)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines from array post_stop, got %d: %q", len(lines), string(data))
	}
}

func TestInterpolateCommand(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		port int
		want string
	}{
		{"no tokens", "npm run dev", 3000, "npm run dev"},
		{"single port", "npx vite --port {port} --host", 3000, "npx vite --port 3000 --host"},
		{"django", "python manage.py runserver 0.0.0.0:{port}", 8000, "python manage.py runserver 0.0.0.0:8000"},
		{"port_2", "cmd --port {port} --ws {port_2}", 3000, "cmd --port 3000 --ws 3001"},
		{"port_3", "cmd {port} {port_2} {port_3}", 5000, "cmd 5000 5001 5002"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := interpolateCommand(tt.cmd, tt.port)
			if got != tt.want {
				t.Errorf("interpolateCommand(%q, %d) = %q, want %q", tt.cmd, tt.port, got, tt.want)
			}
		})
	}
}
