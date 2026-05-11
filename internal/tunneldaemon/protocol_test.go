package tunneldaemon

import (
	"encoding/json"
	"testing"
)

func TestRegisterRoundtrip(t *testing.T) {
	in := Register{Op: OpRegister, Hostname: "salt-main.gtltunnel.dev", Port: 3050}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Register
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", out, in)
	}
}

func TestEventRoundtrip(t *testing.T) {
	cases := []Event{
		{Kind: EventRegistered, Hostname: "x.example.dev", Port: 3050},
		{Kind: EventLog, Stream: StreamStderr, Line: "ERR something broke"},
		{Kind: EventError, Error: "registration refused"},
		{Kind: EventTunnelUp, Line: "INF Registered tunnel connection"},
	}
	for _, in := range cases {
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		var out Event
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		if out != in {
			t.Errorf("roundtrip mismatch: got %+v, want %+v", out, in)
		}
	}
}

func TestClassifyLine(t *testing.T) {
	cases := []struct {
		line string
		want lineClass
	}{
		{"2024 ERR cloudflared failed", lineError},
		{"2024 WRN retrying", lineError},
		{"connection error: timeout", lineError},
		{"2024 INF Registered tunnel connection", lineRegistered},
		{"GET /api/health 200 12ms", lineRequest},
		{"POST https://x.dev/webhook 201", lineRequest},
		{"2024 INF Starting tunnel", lineDrop},
		{"random unrelated line", lineDrop},
	}
	for _, tc := range cases {
		if got := classifyLine(tc.line); got != tc.want {
			t.Errorf("classifyLine(%q) = %d, want %d", tc.line, got, tc.want)
		}
	}
}
