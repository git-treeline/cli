package cmd

import "testing"

func TestBuildOpenURL(t *testing.T) {
	tests := []struct {
		name         string
		port         int
		project      string
		branch       string
		domain       string
		routerPort   int
		svcRunning   bool
		pfConfigured bool
		want         string
	}{
		{
			name:   "plain HTTP when service not running",
			port:   3010,
			branch: "feature-x",
			want:   "http://localhost:3010",
		},
		{
			name:   "plain HTTP when no branch",
			port:   3010,
			branch: "",
			want:   "http://localhost:3010",
		},
		{
			name:         "HTTPS with port forward",
			port:         3010,
			project:      "salt",
			branch:       "feature/staff-reporting",
			domain:       "localhost",
			routerPort:   443,
			svcRunning:   true,
			pfConfigured: true,
			want:         "https://salt-staff-reporting.localhost",
		},
		{
			name:       "HTTPS without port forward includes router port",
			port:       3010,
			project:    "salt",
			branch:     "main",
			domain:     "localhost",
			routerPort: 4430,
			svcRunning: true,
			want:       "https://salt-main.localhost:4430",
		},
		{
			name:       "custom domain",
			port:       3010,
			project:    "myapp",
			branch:     "develop",
			domain:     "dev.local",
			routerPort: 8443,
			svcRunning: true,
			want:       "https://myapp-develop.dev.local:8443",
		},
		{
			name:         "custom domain with port forward",
			port:         3010,
			project:      "myapp",
			branch:       "main",
			domain:       "dev.local",
			routerPort:   443,
			svcRunning:   true,
			pfConfigured: true,
			want:         "https://myapp-main.dev.local",
		},
		{
			name:         "service running but empty branch falls back to HTTP",
			port:         5000,
			project:      "api",
			branch:       "",
			domain:       "localhost",
			routerPort:   443,
			svcRunning:   true,
			pfConfigured: true,
			want:         "http://localhost:5000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildOpenURL(tt.port, tt.project, tt.branch, tt.domain, tt.routerPort, tt.svcRunning, tt.pfConfigured)
			if got != tt.want {
				t.Errorf("buildOpenURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
