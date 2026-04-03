package confirm

import (
	"bufio"
	"fmt"
	"io"
	"os"
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
