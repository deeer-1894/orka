package filesystem

import "testing"

func TestCatalogDoesNotRequireWorkspace(t *testing.T) {
	entries := Catalog()
	if len(entries) != 3 {
		t.Fatalf("catalog=%v", entries)
	}
	if entries[1].Name != "file_write" || entries[1].Schema["properties"].(map[string]any)["mode"] == nil {
		t.Fatal("catalog lost the shared file intent contract")
	}
}
