package envparse

import (
	"bufio"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var lineRE = regexp.MustCompile(`^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)

// Entry represents a single key=value pair from an env file.
type Entry struct {
	Key string
	Val string
}

// ParseFile reads an env file and returns all key=value entries,
// skipping blank lines and comments.
func ParseFile(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var entries []Entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := lineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		entries = append(entries, Entry{Key: m[1], Val: StripQuotes(m[2])})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// StripQuotes removes surrounding single or double quotes from an env value.
func StripQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if s[0] == '"' && s[len(s)-1] == '"' {
			if u, err := strconv.Unquote(s); err == nil {
				return u
			}
		}
		if s[0] == '\'' && s[len(s)-1] == '\'' {
			return strings.ReplaceAll(s[1:len(s)-1], `\'`, `'`)
		}
	}
	return s
}
