package cmd

import "testing"

func TestParsePRNumber(t *testing.T) {
	tests := []struct {
		name    string
		arg     string
		want    int
		wantErr bool
	}{
		{name: "bare number", arg: "473", want: 473},
		{name: "leading hash", arg: "#473", want: 473},
		{name: "trailing whitespace", arg: "#473 ", want: 473},
		{name: "surrounding whitespace", arg: "  473\n", want: 473},
		{name: "hash and trailing whitespace bare", arg: "473\t", want: 473},
		{name: "single digit", arg: "5", want: 5},
		{name: "single digit with hash", arg: "#5", want: 5},
		{name: "non-numeric", arg: "abc", wantErr: true},
		{name: "hash only", arg: "#", wantErr: true},
		{name: "empty", arg: "", wantErr: true},
		{name: "hash with non-numeric", arg: "#abc", wantErr: true},
		{name: "double hash", arg: "##473", wantErr: true},
		{name: "zero", arg: "0", wantErr: true},
		{name: "zero with hash", arg: "#0", wantErr: true},
		{name: "negative", arg: "-1", wantErr: true},
		{name: "negative with hash", arg: "#-1", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePRNumber(tt.arg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parsePRNumber(%q) = %d, want error", tt.arg, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePRNumber(%q) unexpected error: %v", tt.arg, err)
			}
			if got != tt.want {
				t.Errorf("parsePRNumber(%q) = %d, want %d", tt.arg, got, tt.want)
			}
		})
	}
}
