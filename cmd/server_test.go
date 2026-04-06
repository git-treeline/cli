package cmd

import "testing"

func TestInterpolateCommand(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		port int
		want string
	}{
		{"no tokens", "npm run dev", 3000, "npm run dev"},
		{"single port", "npx vite --port {port} --host", 3000, "npx vite --port 3000 --host"},
		{"django", "python manage.py runserver 0.0.0.0:{port}", 8000, "python manage.py runserver 0.0.0.0:8000"},
		{"port_2", "cmd --port {port} --ws {port_2}", 3000, "cmd --port 3000 --ws 3001"},
		{"port_3", "cmd {port} {port_2} {port_3}", 5000, "cmd 5000 5001 5002"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := interpolateCommand(tt.cmd, tt.port)
			if got != tt.want {
				t.Errorf("interpolateCommand(%q, %d) = %q, want %q", tt.cmd, tt.port, got, tt.want)
			}
		})
	}
}
