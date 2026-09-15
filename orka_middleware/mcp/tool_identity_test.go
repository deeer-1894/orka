package mcp

import (
	"strings"
	"testing"
)

func TestExternalToolIdentityIsSourceQualified(t *testing.T) {
	a := qualifiedToolName("source-a", "file_read")
	b := qualifiedToolName("source-b", "file_read")
	if a == b || a == "file_read" || !strings.HasPrefix(a, "mcp_") {
		t.Fatal("identity collision")
	}
	if qualifiedToolName("", "file_read") != "file_read" {
		t.Fatal("builtin renamed")
	}
	if len(qualifiedToolName("source-a", strings.Repeat("long-name", 30))) > 64 {
		t.Fatal("provider name limit")
	}
	if qualifiedToolName("source-a", "a b") == qualifiedToolName("source-a", "a_b") {
		t.Fatal("normalized names collided")
	}
}
