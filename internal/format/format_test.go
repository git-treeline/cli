package format

import "testing"

func TestJoinInts(t *testing.T) {
	tests := []struct {
		ints []int
		sep  string
		want string
	}{
		{[]int{1, 2, 3}, ", ", "1, 2, 3"},
		{[]int{3000}, ",", "3000"},
		{[]int{}, ", ", ""},
		{nil, ", ", ""},
	}
	for _, tt := range tests {
		got := JoinInts(tt.ints, tt.sep)
		if got != tt.want {
			t.Errorf("JoinInts(%v, %q) = %q, want %q", tt.ints, tt.sep, got, tt.want)
		}
	}
}

func TestGetPorts_FromPortsArray(t *testing.T) {
	a := Allocation{
		"ports": []any{float64(3000), float64(3001)},
	}
	ports := GetPorts(a)
	if len(ports) != 2 || ports[0] != 3000 || ports[1] != 3001 {
		t.Errorf("GetPorts = %v, want [3000, 3001]", ports)
	}
}

func TestGetPorts_FromSinglePort(t *testing.T) {
	a := Allocation{
		"port": float64(3010),
	}
	ports := GetPorts(a)
	if len(ports) != 1 || ports[0] != 3010 {
		t.Errorf("GetPorts = %v, want [3010]", ports)
	}
}

func TestGetPorts_Empty(t *testing.T) {
	a := Allocation{}
	ports := GetPorts(a)
	if ports != nil {
		t.Errorf("GetPorts = %v, want nil", ports)
	}
}

func TestGetStr(t *testing.T) {
	a := Allocation{
		"project":  "myapp",
		"database": "myapp_dev",
	}
	if got := GetStr(a, "project"); got != "myapp" {
		t.Errorf("GetStr(project) = %q, want %q", got, "myapp")
	}
	if got := GetStr(a, "missing"); got != "" {
		t.Errorf("GetStr(missing) = %q, want empty", got)
	}
}

func TestPortDisplay(t *testing.T) {
	tests := []struct {
		name string
		a    Allocation
		want string
	}{
		{"with ports", Allocation{"ports": []any{float64(3000)}}, ":3000"},
		{"with port", Allocation{"port": float64(3010)}, ":3010"},
		{"empty", Allocation{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PortDisplay(tt.a); got != tt.want {
				t.Errorf("PortDisplay = %q, want %q", got, tt.want)
			}
		})
	}
}
