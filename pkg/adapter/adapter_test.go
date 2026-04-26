package adapter

import "testing"

func TestPublicAdapterRegistry(t *testing.T) {
	a := Get("claude-code")
	if a == nil {
		t.Fatal("claude-code adapter not registered through public package")
	}
	if a.CassConnector() != "claude_code" {
		t.Fatalf("cass connector = %q, want claude_code", a.CassConnector())
	}
}

func TestPublicNewGeneric(t *testing.T) {
	a := NewGeneric("test-agent", "test-agent", "test_connector", "--flag")
	if a.Name() != "test-agent" {
		t.Fatalf("name = %q, want test-agent", a.Name())
	}
	if a.CassConnector() != "test_connector" {
		t.Fatalf("cass connector = %q, want test_connector", a.CassConnector())
	}
}
