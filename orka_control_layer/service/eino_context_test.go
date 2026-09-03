package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
	filesystem2 "github.com/orka-oss/orka_middleware/local/filesystem"
)

// TestWorkspaceBackendOffload verifies that oversized tool output offloaded by
// the reduction middleware lands in the caller's OWN workspace, so the agent can
// retrieve it with the file_read tool Orka already exposes (an in-memory backend
// would point the model at a file none of Orka's tools can open).
func TestWorkspaceBackendOffload(t *testing.T) {
	base := t.TempDir()
	email := "ctx@test.com"
	be := newWorkspaceBackend(base, email)
	if be == nil {
		t.Fatal("expected a backend when storage is configured")
	}
	ctx := context.Background()
	body := strings.Repeat("report line\n", 500)

	if err := be.Write(ctx, &filesystem.WriteRequest{FilePath: "call-123", Content: body}); err != nil {
		t.Fatalf("write: %v", err)
	}

	// It must be a real file inside the user's workspace, under the offload dir.
	onDisk := filepath.Join(base, email, offloadDir, "call-123")
	if _, err := os.Stat(onDisk); err != nil {
		t.Fatalf("offloaded content is not in the user's workspace: %v", err)
	}

	got, err := be.Read(ctx, &filesystem.ReadRequest{FilePath: filepath.Join(offloadDir, "call-123")})
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(got.Content, "report line") {
		t.Fatalf("read back wrong content: %.40q", got.Content)
	}

	// Line windowing (how an agent pages through a big offloaded file).
	win, err := be.Read(ctx, &filesystem.ReadRequest{FilePath: filepath.Join(offloadDir, "call-123"), Offset: 2, Limit: 3})
	if err != nil {
		t.Fatalf("windowed read: %v", err)
	}
	if n := len(strings.Split(win.Content, "\n")); n != 3 {
		t.Fatalf("windowed read returned %d lines, want 3", n)
	}
}

// TestWorkspaceBackendConfinement: a traversal path must not escape the caller's
// storage root — offload paths are derived from model-supplied tool call ids.
func TestWorkspaceBackendConfinement(t *testing.T) {
	base := t.TempDir()
	be := &workspaceBackend{root: filepath.Join(base, "victim")}
	if _, err := be.Read(context.Background(), &filesystem.ReadRequest{FilePath: "../../etc/passwd"}); err == nil {
		t.Fatal("traversal outside the workspace root must be rejected")
	}
}

// TestContextHandlersBuild asserts the P1 chain actually constructs (a silent
// build failure would leave runs with no context management at all).
func TestContextHandlersBuild(t *testing.T) {
	mw := contextHandlers(context.Background(), t.TempDir(), "ctx@test.com", "test", nil, nil)
	if len(mw) < 2 {
		t.Fatalf("expected patchtoolcalls + reduction handlers, got %d", len(mw))
	}
}

// A sub-agent's result is the most expensive output in the system — a whole
// nested agent run. Clearing it as "an old tool result" cost the worst run
// measured here: four researchers returned sourced reports in 140 seconds, the
// reducer replaced them with placeholders, and the orchestrator spent 680
// seconds re-reading unrelated files with nothing left to synthesise.
func TestSubAgentResultsAreProtectedFromReduction(t *testing.T) {
	protected := map[string]bool{}
	for _, n := range append(protectedToolOutputs(), subAgentNames(nil)...) {
		protected[n] = true
	}
	for _, sp := range DefaultSubAgents() {
		if sp.Name != "" && !protected[sp.Name] {
			t.Errorf("sub-agent %q may be cleared from context", sp.Name)
		}
	}
	// The pipeline's own control tools must stay protected too.
	for _, n := range []string{"validate_factor", "factor_agreement", planToolName} {
		if !protected[n] {
			t.Errorf("%q lost its protection", n)
		}
	}
}

// config.yaml documents a custom sub_agents registry and BuildEinoSubAgentTools
// honours it, so protection keyed to the BUILT-IN names left every custom
// delegate exposed to exactly the failure the built-ins are protected from.
func TestCustomSubAgentsAreProtected(t *testing.T) {
	specs := []config.SubAgentConfig{
		{Name: "analyst", Tools: []string{"file_read"}},
		{Name: "auditor", Tools: []string{"file_read"}},
	}
	got := map[string]bool{}
	for _, n := range subAgentNames(specs) {
		got[n] = true
	}
	for _, sp := range specs {
		if !got[sp.Name] {
			t.Errorf("configured sub-agent %q is not protected from reduction", sp.Name)
		}
	}
	if got["researcher"] {
		t.Error("built-in names leaked in when a custom registry is configured")
	}
}

// Delegates ran with no context management at all: BuildEinoSubAgentTools
// installed a budget guard and nothing else, so a researcher's own retrieval
// (fetch_url averages 3k chars here, http_request 14k, over as many as 15 calls)
// accumulated verbatim until the budget cut it off mid-work.
func TestSubAgentsGetContextHandlers(t *testing.T) {
	ctx := withOffloadRoot(context.Background(), t.TempDir())
	tools := []agent.BaseTool{gateStubTool{name: "fetch_url"}, gateStubTool{name: "web_search"}}

	mw := subAgentContextHandlers(ctx, "researcher", tools)
	if len(mw) < 2 {
		t.Fatalf("a delegate got %d context handlers, want patchtoolcalls + reduction", len(mw))
	}
}

// Without storage there is nowhere to offload to, so a delegate must be left
// alone rather than handed a reducer that points at files it cannot write.
func TestSubAgentContextHandlersNeedStorage(t *testing.T) {
	if mw := subAgentContextHandlers(context.Background(), "researcher", nil); mw != nil {
		t.Fatalf("expected no handlers without an offload root, got %d", len(mw))
	}
}

// The offload root has to survive onto the context the orchestrator is BUILT
// from, or delegates silently fall back to no reduction again.
func TestOffloadRootRoundTrips(t *testing.T) {
	if got := offloadRootFrom(context.Background()); got != "" {
		t.Fatalf("unset root should be empty, got %q", got)
	}
	ctx := withOffloadRoot(context.Background(), "/data/storage")
	if got := offloadRootFrom(ctx); got != "/data/storage" {
		t.Fatalf("root did not survive the context: %q", got)
	}
	// An empty base must not install a value that later reads as configured.
	if got := offloadRootFrom(withOffloadRoot(context.Background(), "")); got != "" {
		t.Fatalf("empty base should stay unset, got %q", got)
	}
}

// The path an offloaded result is advertised at must be the path file_read
// opens. These diverged: eino named files "/tmp/clear/{call_id}" while the
// backend stored them under .orka_offload/, so the model was handed a path that
// resolves inside its workspace to a file that was never written — every
// offloaded result was unreachable.
func TestOffloadPathIsReadableByFileRead(t *testing.T) {
	base, email := t.TempDir(), "ctx@test.com"
	be := newWorkspaceBackend(base, email)
	root := filepath.Join(base, email)

	gen := offloadPathFor("clear")
	advertised, err := gen(context.Background(), &reduction.ToolDetail{
		ToolContext: &adk.ToolContext{Name: "researcher", CallID: "call_a1b2c3"},
	})
	if err != nil {
		t.Fatalf("gen path: %v", err)
	}
	if err := be.Write(context.Background(), &filesystem.WriteRequest{
		FilePath: advertised, Content: "conclusion: X\nbody",
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Exactly what the agent does with the advertised path.
	fileRead := filesystem2.New(root)[0]
	if fileRead.Name() != readFileToolName {
		t.Fatalf("expected %s tool first, got %s", readFileToolName, fileRead.Name())
	}
	out, err := fileRead.Invoke(context.Background(), map[string]any{"path": advertised})
	if err != nil {
		t.Fatalf("file_read on the advertised path %q failed: %v", advertised, err)
	}
	if !strings.Contains(out, "conclusion: X") {
		t.Fatalf("read back wrong content: %.60q", out)
	}
	// The name should say what it holds, so file_list reads as an index.
	if !strings.Contains(advertised, "researcher") {
		t.Errorf("offload path %q does not name the tool that produced it", advertised)
	}
}

// Clearing keeps the tool CALL but used to reduce the result to a bare pointer.
// The abstract is what lets the orchestrator synthesise without re-reading, and
// decide when re-reading is worth it.
func TestClearKeepsAnAbstractOfTheResult(t *testing.T) {
	body := "结论:X 优于 Y,置信度中等。\n" + strings.Repeat("supporting detail line\n", 400)
	res, err := l0ClearHandler()(context.Background(), &reduction.ToolDetail{
		ToolContext:  &adk.ToolContext{Name: "researcher", CallID: "call_z9"},
		ToolArgument: &schema.ToolArgument{Text: `{"brief":"compare X and Y"}`},
		ToolResult: &schema.ToolResult{Parts: []schema.ToolOutputPart{
			{Type: schema.ToolPartTypeText, Text: body},
		}},
	})
	if err != nil {
		t.Fatalf("clear handler: %v", err)
	}
	if !res.NeedClear || !res.NeedOffload {
		t.Fatalf("expected clear+offload, got clear=%v offload=%v", res.NeedClear, res.NeedOffload)
	}
	placeholder := joinToolText(res.ToolResult.Parts)
	if !strings.Contains(placeholder, "结论:X 优于 Y") {
		t.Errorf("placeholder carries no gist of the result:\n%s", placeholder)
	}
	if !strings.Contains(placeholder, res.OffloadFilePath) {
		t.Errorf("placeholder does not point at the archive it wrote")
	}
	// It has to be far smaller than what it replaces, or it is not a reduction.
	if len([]rune(placeholder)) > len([]rune(body))/4 {
		t.Errorf("placeholder is %d runes against a %d-rune result", len([]rune(placeholder)), len([]rune(body)))
	}
	// The request survives so the model still knows what was asked.
	if res.ToolArgument == nil || !strings.Contains(res.ToolArgument.Text, "compare X and Y") {
		t.Error("clearing dropped the tool call arguments")
	}
	// The archive explains itself to whoever finds it later.
	if !strings.Contains(res.OffloadContent, "researcher") || !strings.Contains(res.OffloadContent, "supporting detail line") {
		t.Error("offloaded body lost either its header or its content")
	}
}

// An empty result has nothing to reclaim; clearing it would spend a placeholder
// to save nothing and point at an empty file.
func TestClearSkipsEmptyResults(t *testing.T) {
	res, err := l0ClearHandler()(context.Background(), &reduction.ToolDetail{
		ToolContext: &adk.ToolContext{Name: "file_write", CallID: "c1"},
		ToolResult:  &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: "   \n"}}},
	})
	if err != nil {
		t.Fatalf("clear handler: %v", err)
	}
	if res.NeedClear {
		t.Error("an empty tool result should not be cleared")
	}
}

// Offload paths are built from model- and provider-supplied ids; they must stay
// one segment inside the offload dir.
func TestOffloadPathConfinement(t *testing.T) {
	p, err := offloadPathFor("clear")(context.Background(), &reduction.ToolDetail{
		ToolContext: &adk.ToolContext{Name: "../../etc", CallID: "../../passwd"},
	})
	if err != nil {
		t.Fatalf("gen path: %v", err)
	}
	if strings.Contains(p, "..") {
		t.Fatalf("traversal survived into the offload path: %q", p)
	}
	if !strings.HasPrefix(p, offloadDir) {
		t.Fatalf("offload path escaped the offload dir: %q", p)
	}
	// Long or exotic names must still yield a plain, writable filename.
	long, err := offloadPathFor("clear")(context.Background(), &reduction.ToolDetail{
		ToolContext: &adk.ToolContext{Name: strings.Repeat("研究员", 40), CallID: strings.Repeat("z", 200)},
	})
	if err != nil {
		t.Fatalf("gen path: %v", err)
	}
	for _, r := range filepath.Base(long) {
		if r > 127 {
			t.Fatalf("non-ASCII rune %q leaked into filename %q", r, long)
		}
	}
}
