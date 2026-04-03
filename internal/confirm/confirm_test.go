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
	// reader is nil because it shouldn't be used
	if !Prompt("Continue?", true, nil) {
		t.Error("expected true when force=true")
	}
}
