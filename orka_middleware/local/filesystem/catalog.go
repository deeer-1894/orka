package filesystem

// ToolMetadata is intentionally not executable. Catalog callers need neither an
// owner nor a conversation and must not construct an execution workspace.
type ToolMetadata struct {
	Name        string
	Description string
	Schema      map[string]any
}

// Catalog derives metadata from the adapters without opening or creating files.
func Catalog() []ToolMetadata {
	tools := New("") // constructors only; invocation objects never escape this method
	out := make([]ToolMetadata, 0, len(tools))
	for _, tool := range tools {
		out = append(out, ToolMetadata{tool.Name(), tool.Description(), tool.Schema()})
	}
	return out
}
