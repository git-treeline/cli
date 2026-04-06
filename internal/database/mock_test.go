package database

import (
	"fmt"
	"testing"
)

type mockAdapter struct {
	calls    []string
	existing map[string]bool
	failOn   string
}

func newMockAdapter() *mockAdapter {
	return &mockAdapter{existing: map[string]bool{}}
}

func (m *mockAdapter) Clone(template, target string) error {
	m.calls = append(m.calls, fmt.Sprintf("clone:%s->%s", template, target))
	if m.failOn == "clone" {
		return fmt.Errorf("clone failed")
	}
	m.existing[target] = true
	return nil
}

func (m *mockAdapter) Drop(target string) error {
	m.calls = append(m.calls, fmt.Sprintf("drop:%s", target))
	if m.failOn == "drop" {
		return fmt.Errorf("drop failed")
	}
	delete(m.existing, target)
	return nil
}

func (m *mockAdapter) Exists(name string) (bool, error) {
	m.calls = append(m.calls, fmt.Sprintf("exists:%s", name))
	return m.existing[name], nil
}

func (m *mockAdapter) Restore(target, dumpFile string) error {
	m.calls = append(m.calls, fmt.Sprintf("restore:%s<-%s", target, dumpFile))
	if m.failOn == "restore" {
		return fmt.Errorf("restore failed")
	}
	m.existing[target] = true
	return nil
}

func TestMockAdapter_ImplementsInterface(t *testing.T) {
	var _ Adapter = newMockAdapter()
}

func TestMockAdapter_ResetSequence_DropThenClone(t *testing.T) {
	m := newMockAdapter()
	m.existing["myapp_dev_feat"] = true

	if err := m.Drop("myapp_dev_feat"); err != nil {
		t.Fatal(err)
	}
	if err := m.Clone("myapp_dev", "myapp_dev_feat"); err != nil {
		t.Fatal(err)
	}

	if len(m.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d: %v", len(m.calls), m.calls)
	}
	if m.calls[0] != "drop:myapp_dev_feat" {
		t.Errorf("first call: want drop, got %s", m.calls[0])
	}
	if m.calls[1] != "clone:myapp_dev->myapp_dev_feat" {
		t.Errorf("second call: want clone, got %s", m.calls[1])
	}
	if !m.existing["myapp_dev_feat"] {
		t.Error("expected target to exist after clone")
	}
}

func TestMockAdapter_RestoreSequence_DropThenRestore(t *testing.T) {
	m := newMockAdapter()
	m.existing["myapp_dev_feat"] = true

	if err := m.Drop("myapp_dev_feat"); err != nil {
		t.Fatal(err)
	}
	if err := m.Restore("myapp_dev_feat", "/tmp/dump.sql"); err != nil {
		t.Fatal(err)
	}

	if len(m.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(m.calls))
	}
	if m.calls[0] != "drop:myapp_dev_feat" {
		t.Errorf("first call: want drop, got %s", m.calls[0])
	}
	if m.calls[1] != "restore:myapp_dev_feat<-/tmp/dump.sql" {
		t.Errorf("second call: want restore, got %s", m.calls[1])
	}
}

func TestMockAdapter_DropNonexistent_NoError(t *testing.T) {
	m := newMockAdapter()
	if err := m.Drop("nonexistent"); err != nil {
		t.Fatalf("expected no error for dropping nonexistent db, got: %v", err)
	}
}

func TestMockAdapter_DropFailure_PropagatesError(t *testing.T) {
	m := newMockAdapter()
	m.failOn = "drop"

	err := m.Drop("myapp")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMockAdapter_CloneFailure_PropagatesError(t *testing.T) {
	m := newMockAdapter()
	m.failOn = "clone"

	err := m.Clone("template", "target")
	if err == nil {
		t.Fatal("expected error")
	}
	if m.existing["target"] {
		t.Error("target should not exist after failed clone")
	}
}

func TestMockAdapter_RestoreFailure_PropagatesError(t *testing.T) {
	m := newMockAdapter()
	m.failOn = "restore"

	err := m.Restore("myapp", "/tmp/dump.sql")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMockAdapter_ExistsAfterClone(t *testing.T) {
	m := newMockAdapter()

	exists, _ := m.Exists("newdb")
	if exists {
		t.Error("should not exist before clone")
	}

	_ = m.Clone("template", "newdb")

	exists, _ = m.Exists("newdb")
	if !exists {
		t.Error("should exist after clone")
	}
}

func TestMockAdapter_ExistsAfterDropIsGone(t *testing.T) {
	m := newMockAdapter()
	m.existing["mydb"] = true

	exists, _ := m.Exists("mydb")
	if !exists {
		t.Error("should exist initially")
	}

	_ = m.Drop("mydb")

	exists, _ = m.Exists("mydb")
	if exists {
		t.Error("should not exist after drop")
	}
}
