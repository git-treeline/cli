// Package tunneldaemon implements a lazy-start supervisor that lets multiple
// `gtl tunnel` invocations share a single cloudflared process for one named
// tunnel. The daemon owns the cloudflared lifecycle; clients register a
// hostname+port and receive log events over a Unix socket. When the last
// client disconnects, the daemon shuts down cloudflared and exits.
package tunneldaemon

// Wire protocol: newline-delimited JSON over a Unix socket.
// First message client -> daemon is Register. Daemon replies with one
// Event{Kind:"registered"} (or {Kind:"error"} and closes). Daemon then
// pushes Event records as cloudflared produces output. Client closes the
// connection to unregister; daemon detects EOF and removes the hostname.

type Register struct {
	Op       string `json:"op"`
	Hostname string `json:"hostname"`
	Port     int    `json:"port"`
}

type Event struct {
	Kind     string `json:"kind"`
	Hostname string `json:"hostname,omitempty"`
	Port     int    `json:"port,omitempty"`
	Line     string `json:"line,omitempty"`
	Stream   string `json:"stream,omitempty"`
	Error    string `json:"error,omitempty"`
}

const (
	OpRegister = "register"

	EventRegistered = "registered"
	EventLog        = "log"
	EventError      = "error"
	EventTunnelUp   = "tunnel_up"
	EventTunnelDown = "tunnel_down"

	StreamStdout = "stdout"
	StreamStderr = "stderr"
)
