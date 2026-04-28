package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Guard tests scan source code for invariant violations that are hard to
// catch in code review. Each test prevents a class of bug rather than an
// individual instance.

func TestGuard_NoOsExitInInternal(t *testing.T) {
	violations := scanGoFiles(t, "../internal", func(fset *token.FileSet, f *ast.File, path string) []string {
		var hits []string
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if ident.Name == "os" && sel.Sel.Name == "Exit" {
				pos := fset.Position(call.Pos())
				hits = append(hits, pos.String())
			}
			return true
		})
		return hits
	})

	if len(violations) > 0 {
		t.Errorf("os.Exit() found in internal/ — return errors to the caller instead:\n  %s",
			strings.Join(violations, "\n  "))
	}
}

func TestGuard_NoFmtPrintInInternal(t *testing.T) {
	allowedFiles := map[string]bool{
		"style.go": true, // style package is the output layer
	}
	allowedPkgs := map[string]bool{
		"tui":       true, // Bubble Tea renders via fmt
		"proxy":     true, // router lifecycle messages (intentional)
		"confirm":   true, // interactive prompts write to stdout
		"format":    true, // output formatting for display
		"share":     true, // CLI-facing share output
		"tunnel":    true, // CLI-facing tunnel output
		"tailscale": true, // CLI-facing tailscale output
		"service":   true, // install/uninstall user feedback
	}

	violations := scanGoFiles(t, "../internal", func(fset *token.FileSet, f *ast.File, path string) []string {
		base := filepath.Base(path)
		if strings.HasSuffix(base, "_test.go") || allowedFiles[base] {
			return nil
		}
		pkg := filepath.Base(filepath.Dir(path))
		if allowedPkgs[pkg] {
			return nil
		}

		var hits []string
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if ident.Name == "fmt" && (sel.Sel.Name == "Print" || sel.Sel.Name == "Println" || sel.Sel.Name == "Printf") {
				pos := fset.Position(call.Pos())
				hits = append(hits, pos.String())
			}
			return true
		})
		return hits
	})

	if len(violations) > 0 {
		t.Errorf("fmt.Print*/Println/Printf found in internal/ (use fmt.Fprint to stderr, or internal/style for user output):\n  %s",
			strings.Join(violations, "\n  "))
	}
}

func TestGuard_NoOsExitInCmdRunE(t *testing.T) {
	violations := scanGoFiles(t, ".", func(fset *token.FileSet, f *ast.File, path string) []string {
		base := filepath.Base(path)
		if base == "root.go" || strings.HasSuffix(base, "_test.go") || base == "guard_test.go" {
			return nil
		}

		var hits []string
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if ident.Name == "os" && sel.Sel.Name == "Exit" {
				pos := fset.Position(call.Pos())
				hits = append(hits, pos.String())
			}
			return true
		})
		return hits
	})

	if len(violations) > 0 {
		t.Errorf("os.Exit() found in cmd/ (only root.go may call os.Exit):\n  %s",
			strings.Join(violations, "\n  "))
	}
}

// TestGuard_CliErrorUsesCliErr ensures all CliError returns in RunE functions
// use cliErr() to suppress usage output for domain errors.
func TestGuard_CliErrorUsesCliErr(t *testing.T) {
	violations := scanGoFiles(t, ".", func(fset *token.FileSet, f *ast.File, path string) []string {
		base := filepath.Base(path)
		if strings.HasSuffix(base, "_test.go") || base == "error.go" {
			return nil
		}

		var hits []string

		ast.Inspect(f, func(n ast.Node) bool {
			// Look for RunE function literals in cobra.Command definitions
			if kv, ok := n.(*ast.KeyValueExpr); ok {
				if ident, ok := kv.Key.(*ast.Ident); ok && ident.Name == "RunE" {
					ast.Inspect(kv.Value, func(inner ast.Node) bool {
						if ret, ok := inner.(*ast.ReturnStmt); ok {
							for _, result := range ret.Results {
								if isBareCLIError(result) {
									pos := fset.Position(ret.Pos())
									hits = append(hits, pos.String())
								}
							}
						}
						return true
					})
					return false
				}
			}
			return true
		})
		return hits
	})

	if len(violations) > 0 {
		t.Errorf("Bare CliError returns found in RunE (use cliErr(cmd, ...) to suppress usage):\n  %s",
			strings.Join(violations, "\n  "))
	}
}

func TestGuard_OnboardingCommandsDoNotRequireServeInstalled(t *testing.T) {
	for _, file := range []string{"setup.go", "new.go", "clone.go", "review.go"} {
		t.Run(file, func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "requireServeInstalled()") {
				t.Fatalf("%s should not hard-require the HTTPS router; use warnServeNotInstalled instead", file)
			}
		})
	}
}

// cliErrHelpers are functions that can return *CliError indirectly. Calls to
// these inside RunE must be wrapped with cliErr(cmd, ...) just like direct
// &CliError{} literals and errXxx() constructors.
var cliErrHelpers = map[string]bool{
	"awaitReady":            true,
	"resolveTunnelTarget":   true,
	"resolveStartHooks":     true,
	"validateTunnelPrereqs": true,
	"switchWorktreeBranch":  true,
}

// isBareCLIError returns true if the expression is a direct &CliError{},
// errXxx() call, or known CliError-producing helper that's not wrapped in
// cliErr().
func isBareCLIError(expr ast.Expr) bool {
	// Check for &CliError{...}
	if unary, ok := expr.(*ast.UnaryExpr); ok {
		if comp, ok := unary.X.(*ast.CompositeLit); ok {
			if ident, ok := comp.Type.(*ast.Ident); ok && ident.Name == "CliError" {
				return true
			}
		}
	}

	if call, ok := expr.(*ast.CallExpr); ok {
		if ident, ok := call.Fun.(*ast.Ident); ok {
			// errXxx constructors should be wrapped
			if strings.HasPrefix(ident.Name, "err") && ident.Name != "err" {
				return true
			}
			// Known helpers that return *CliError
			if cliErrHelpers[ident.Name] {
				return true
			}
		}
	}

	return false
}

type inspectFunc func(fset *token.FileSet, f *ast.File, path string) []string

func scanGoFiles(t *testing.T, dir string, fn inspectFunc) []string {
	t.Helper()
	var all []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		fset := token.NewFileSet()
		f, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil
		}
		all = append(all, fn(fset, f, path)...)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	return all
}
