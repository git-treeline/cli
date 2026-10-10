// Package routerdomain validates the DNS suffix used by the local HTTPS router.
package routerdomain

import (
	"fmt"
	"net"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// Validate returns the canonical router suffix or an error. Router domains
// become DNS name constraints on a locally trusted CA, so they must identify
// a narrow suffix rather than an IP address, wildcard, or public suffix.
func Validate(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("router domain must not be empty")
	}
	if value != strings.TrimSpace(value) {
		return "", fmt.Errorf("router domain must not contain surrounding whitespace")
	}
	if strings.ContainsAny(value, "\r\n\t") {
		return "", fmt.Errorf("router domain must not contain control characters")
	}
	if strings.Contains(value, "*") {
		return "", fmt.Errorf("router domain must be a DNS suffix without wildcards")
	}
	if net.ParseIP(value) != nil {
		return "", fmt.Errorf("router domain must be a DNS suffix, not an IP address")
	}

	domain := strings.ToLower(value)
	if domain == "localhost" {
		return domain, nil
	}
	if len(domain) > 253 || strings.HasSuffix(domain, ".") {
		return "", fmt.Errorf("router domain must be a valid DNS suffix")
	}

	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("router domain %q is too broad; use localhost or a registrable DNS suffix", value)
	}
	for _, label := range labels {
		if !validLabel(label) {
			return "", fmt.Errorf("router domain %q is not a valid DNS suffix", value)
		}
	}
	if allNumericLabels(labels) {
		return "", fmt.Errorf("router domain must be a DNS suffix, not an IP address")
	}
	if suffix, _ := publicsuffix.PublicSuffix(domain); suffix == domain {
		return "", fmt.Errorf("router domain %q is a public suffix and would grant the local CA overly broad authority", value)
	}
	return domain, nil
}

func allNumericLabels(labels []string) bool {
	for _, label := range labels {
		for _, c := range label {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

func validLabel(label string) bool {
	if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, c := range label {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}
