package database

import "testing"

func TestParsePsqlListContains(t *testing.T) {
	// Realistic psql -lqt output
	output := ` myapp_development   | user | UTF8     | libc            | en_US.UTF-8 | en_US.UTF-8 |            |           |
 myapp_test          | user | UTF8     | libc            | en_US.UTF-8 | en_US.UTF-8 |            |           |
 postgres            | user | UTF8     | libc            | en_US.UTF-8 | en_US.UTF-8 |            |           |
 template0           | user | UTF8     | libc            | en_US.UTF-8 | en_US.UTF-8 |            |           | =c/user          +
                     |      |          |                 |             |             |            |           | user=CTc/user
 template1           | user | UTF8     | libc            | en_US.UTF-8 | en_US.UTF-8 |            |           | =c/user          +
                     |      |          |                 |             |             |            |           | user=CTc/user
`

	tests := []struct {
		name   string
		db     string
		expect bool
	}{
		{"existing db", "myapp_development", true},
		{"another existing db", "myapp_test", true},
		{"system db", "postgres", true},
		{"nonexistent", "myapp_staging", false},
		{"partial match", "myapp", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePsqlListContains(output, tt.db)
			if got != tt.expect {
				t.Errorf("parsePsqlListContains(%q) = %v, want %v", tt.db, got, tt.expect)
			}
		})
	}
}

func TestForAdapter(t *testing.T) {
	tests := []struct {
		name      string
		wantErr   bool
		wantType  string
	}{
		{"postgresql", false, "postgresql"},
		{"sqlite", false, "sqlite"},
		{"", false, "postgresql"},
		{"mysql", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter, err := ForAdapter(tt.name)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if adapter == nil {
				t.Fatal("expected non-nil adapter")
			}
		})
	}
}
