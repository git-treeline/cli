package confirm

import (
	"strings"
	"testing"
)

func TestPrompt_Yes(t *testing.T) {
	reader := strings.NewReader("y\n")
	if !Prompt("Continue?", false, reader) {
		t.Error("expected true for 'y' input")
	}
}

func TestPrompt_YesFull(t *testing.T) {
	reader := strings.NewReader("yes\n")
	if !Prompt("Continue?", false, reader) {
		t.Error("expected true for 'yes' input")
	}
}

func TestPrompt_No(t *testing.T) {
	reader := strings.NewReader("n\n")
	if Prompt("Continue?", false, reader) {
		t.Error("expected false for 'n' input")
	}
}

func TestPrompt_Empty(t *testing.T) {
	reader := strings.NewReader("\n")
	if Prompt("Continue?", false, reader) {
		t.Error("expected false for empty input (default No)")
	}
}

func TestPrompt_Force(t *testing.T) {
	if !Prompt("Continue?", true, nil) {
		t.Error("expected true when force=true")
	}
}

func TestInput_CustomValue(t *testing.T) {
	reader := strings.NewReader("hello world\n")
	got := Input("Enter name", "default", reader)
	if got != "hello world" {
		t.Errorf("expected 'hello world', got %q", got)
	}
}

func TestInput_EmptyReturnsDefault(t *testing.T) {
	reader := strings.NewReader("\n")
	got := Input("Enter name", "fallback", reader)
	if got != "fallback" {
		t.Errorf("expected 'fallback', got %q", got)
	}
}

func TestInput_NoDefaultEmptyInput(t *testing.T) {
	reader := strings.NewReader("\n")
	got := Input("Enter name", "", reader)
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestInput_WhitespaceOnlyReturnsDefault(t *testing.T) {
	reader := strings.NewReader("   \n")
	got := Input("Enter name", "def", reader)
	if got != "def" {
		t.Errorf("expected 'def' for whitespace-only input, got %q", got)
	}
}

func TestSelect_ExplicitChoice(t *testing.T) {
	reader := strings.NewReader("2\n")
	got := Select("Pick one", []string{"a", "b", "c"}, 0, reader)
	if got != 1 {
		t.Errorf("expected index 1, got %d", got)
	}
}

func TestSelect_EmptyReturnsDefault(t *testing.T) {
	reader := strings.NewReader("\n")
	got := Select("Pick one", []string{"a", "b", "c"}, 1, reader)
	if got != 1 {
		t.Errorf("expected default index 1, got %d", got)
	}
}

func TestSelect_OutOfRange(t *testing.T) {
	reader := strings.NewReader("99\n")
	got := Select("Pick one", []string{"a", "b"}, 0, reader)
	if got != 0 {
		t.Errorf("expected default index 0 for out-of-range, got %d", got)
	}
}

func TestSelect_NonNumeric(t *testing.T) {
	reader := strings.NewReader("abc\n")
	got := Select("Pick one", []string{"a", "b"}, 0, reader)
	if got != 0 {
		t.Errorf("expected default index 0 for non-numeric, got %d", got)
	}
}

func TestSelect_Zero(t *testing.T) {
	reader := strings.NewReader("0\n")
	got := Select("Pick one", []string{"a", "b"}, 1, reader)
	if got != 1 {
		t.Errorf("expected default index 1 for zero input, got %d", got)
	}
}
