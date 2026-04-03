// Package confirm provides interactive prompts for CLI commands.
package confirm

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Prompt asks the user a y/N question. Returns true if the user types "y" or "yes".
// If force is true, returns true without prompting.
// Reader can be overridden for testing; defaults to os.Stdin.
func Prompt(message string, force bool, reader io.Reader) bool {
	if force {
		return true
	}
	if reader == nil {
		reader = os.Stdin
	}

	fmt.Printf("%s [y/N] ", message)
	scanner := bufio.NewScanner(reader)
	if scanner.Scan() {
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		return answer == "y" || answer == "yes"
	}
	return false
}

// Select presents a numbered list of options and returns the chosen index.
// defaultIdx is pre-selected when the user presses Enter without typing.
// Reader can be overridden for testing; defaults to os.Stdin.
func Select(message string, options []string, defaultIdx int, reader io.Reader) int {
	if reader == nil {
		reader = os.Stdin
	}

	fmt.Println(message)
	for i, opt := range options {
		marker := "  "
		if i == defaultIdx {
			marker = "→ "
		}
		fmt.Printf("  %s[%d] %s\n", marker, i+1, opt)
	}
	fmt.Printf("  Choice [%d]: ", defaultIdx+1)

	scanner := bufio.NewScanner(reader)
	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			return defaultIdx
		}
		if n, err := strconv.Atoi(input); err == nil && n >= 1 && n <= len(options) {
			return n - 1
		}
	}
	return defaultIdx
}
