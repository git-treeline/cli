package routerdomain

import "testing"

func TestValidate(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
		ok    bool
	}{
		{name: "localhost", value: "localhost", want: "localhost", ok: true},
		{name: "canonicalizes case", value: "Dev.PRT.Test", want: "dev.prt.test", ok: true},
		{name: "normal suffix", value: "prt.dev", want: "prt.dev", ok: true},
		{name: "empty", value: "", ok: false},
		{name: "single label public suffix", value: "com", ok: false},
		{name: "multi label public suffix", value: "co.uk", ok: false},
		{name: "private public suffix", value: "github.io", ok: false},
		{name: "ipv4", value: "127.0.0.1", ok: false},
		{name: "abbreviated ipv4", value: "127.1", ok: false},
		{name: "ipv4 with leading zeroes", value: "127.000.000.001", ok: false},
		{name: "ipv6", value: "::1", ok: false},
		{name: "wildcard", value: "*.example.test", ok: false},
		{name: "newline", value: "example.test\n127.0.0.1 evil.test", ok: false},
		{name: "leading hyphen", value: "-bad.test", ok: false},
		{name: "empty label", value: "bad..test", ok: false},
		{name: "absolute name", value: "example.test.", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Validate(tt.value)
			if (err == nil) != tt.ok {
				t.Fatalf("Validate(%q) error = %v, want success %v", tt.value, err, tt.ok)
			}
			if got != tt.want {
				t.Errorf("Validate(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}
