package service

import (
	"strings"
	"testing"
)

const samplePfConf = `scrub-anchor "com.apple/*"
nat-anchor "com.apple/*"
rdr-anchor "com.apple/*"
dummynet-anchor "com.apple/*"
anchor "com.apple/*"
load anchor "com.apple" from "/etc/pf.anchors/com.apple"
`

func TestInsertPfRules(t *testing.T) {
	result := insertPfRules(samplePfConf)

	if !strings.Contains(result, `rdr-anchor "dev.treeline.router"`) {
		t.Error("missing rdr-anchor line")
	}
	if !strings.Contains(result, `load anchor "dev.treeline.router"`) {
		t.Error("missing load anchor line")
	}

	lines := strings.Split(result, "\n")
	rdrAppleIdx := -1
	rdrTreelineIdx := -1
	anchorIdx := -1
	loadIdx := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == `rdr-anchor "com.apple/*"` {
			rdrAppleIdx = i
		}
		if strings.Contains(trimmed, `rdr-anchor "dev.treeline.router"`) {
			rdrTreelineIdx = i
		}
		if trimmed == `anchor "com.apple/*"` {
			anchorIdx = i
		}
		if strings.Contains(trimmed, `load anchor "dev.treeline.router"`) {
			loadIdx = i
		}
	}

	if rdrTreelineIdx <= rdrAppleIdx {
		t.Errorf("rdr-anchor should come after com.apple rdr-anchor (apple=%d, treeline=%d)", rdrAppleIdx, rdrTreelineIdx)
	}
	if rdrTreelineIdx >= anchorIdx {
		t.Errorf("rdr-anchor should come before anchor line (treeline=%d, anchor=%d)", rdrTreelineIdx, anchorIdx)
	}
	if loadIdx <= anchorIdx {
		t.Errorf("load anchor should come at end (load=%d, anchor=%d)", loadIdx, anchorIdx)
	}
}

func TestInsertPfRules_NoExistingRdrAnchor(t *testing.T) {
	minimal := "anchor \"com.apple/*\"\n"
	result := insertPfRules(minimal)

	if !strings.Contains(result, `rdr-anchor "dev.treeline.router"`) {
		t.Error("missing rdr-anchor line")
	}

	lines := strings.Split(result, "\n")
	if !strings.Contains(lines[0], `rdr-anchor "dev.treeline.router"`) {
		t.Error("rdr-anchor should be inserted at the top when no existing rdr-anchor")
	}
}

func TestInsertPfRules_MarkerPresent(t *testing.T) {
	result := insertPfRules(samplePfConf)
	count := strings.Count(result, pfMarker())
	if count != 2 {
		t.Errorf("expected 2 marker comments (rdr-anchor + load anchor), got %d", count)
	}
}

func TestInsertPfRules_TrailingNewline(t *testing.T) {
	result := insertPfRules(samplePfConf)
	if !strings.HasSuffix(result, "\n") {
		t.Error("pf.conf must end with a newline — pfctl will reject it otherwise")
	}
}

func TestGeneratePfAnchor(t *testing.T) {
	content := GeneratePfAnchor(3001)
	if !strings.Contains(content, "rdr pass on lo0") {
		t.Error("missing rdr rule")
	}
	if !strings.Contains(content, "port 443") {
		t.Error("should redirect from port 443")
	}
	if !strings.Contains(content, "port 3001") {
		t.Error("should redirect to port 3001")
	}
}
