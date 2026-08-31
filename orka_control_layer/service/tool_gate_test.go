package service

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func infos(names ...string) []*schema.ToolInfo {
	out := make([]*schema.ToolInfo, 0, len(names))
	for _, n := range names {
		out = append(out, &schema.ToolInfo{Name: n, Desc: "does " + n})
	}
	return out
}

func names(is []*schema.ToolInfo) map[string]bool {
	m := map[string]bool{}
	for _, i := range is {
		m[i.Name] = true
	}
	return m
}

func TestGateShowsCoreHidesRest(t *testing.T) {
	g := newToolGate()
	g.remember(infos("web_search", "file_write", "shell", "csv_join", "slides", "qrcode"))
	got := names(g.visible())
	for _, want := range []string{"web_search", "file_write", "shell"} {
		if !got[want] {
			t.Errorf("core tool %q was hidden", want)
		}
	}
	for _, hidden := range []string{"csv_join", "slides", "qrcode"} {
		if got[hidden] {
			t.Errorf("rare tool %q is still costing tokens every turn", hidden)
		}
	}
}

// Multi-agent delegation runs through the sub-agent tools; hiding them would
// silently turn it off.
func TestGateKeepsGeneralSubAgentsVisible(t *testing.T) {
	g := newToolGate()
	g.remember(infos("researcher", "writer", "browser", "engineer", "factor_proposer"))
	got := names(g.visible())
	for _, want := range []string{"researcher", "writer", "browser", "engineer"} {
		if !got[want] {
			t.Errorf("sub-agent %q hidden — delegation would stop working", want)
		}
	}
	if got["factor_proposer"] {
		t.Error("quant-pipeline agent is visible to every conversation")
	}
}

func TestGateUnlockPersistsForTheRun(t *testing.T) {
	g := newToolGate()
	g.remember(infos("web_search", "csv_join", "sql_query"))
	if names(g.visible())["csv_join"] {
		t.Fatal("csv_join visible before being asked for")
	}
	g.unlock([]string{"csv_join"})
	got := names(g.visible())
	if !got["csv_join"] {
		t.Fatal("unlocked tool did not become visible")
	}
	if got["sql_query"] {
		t.Error("unlocking one tool exposed an unrelated one")
	}
}

// eino persists edits to state.ToolInfos, so the complete list is only visible
// on the first call. Losing it would permanently strand every hidden tool.
func TestGateRemembersOnlyTheFirstFullList(t *testing.T) {
	g := newToolGate()
	g.remember(infos("web_search", "csv_join", "slides"))
	g.remember(infos("web_search")) // the filtered list coming back on turn 2
	if len(g.all) != 3 {
		t.Fatalf("captured %d tools, want the original 3", len(g.all))
	}
	g.unlock([]string{"slides"})
	if !names(g.visible())["slides"] {
		t.Fatal("a hidden tool became unreachable after the second turn")
	}
}

func TestGateSearchNarrowsWithEveryTerm(t *testing.T) {
	g := newToolGate()
	g.all = []*schema.ToolInfo{
		{Name: "csv_join", Desc: "join two csv files on a key"},
		{Name: "csv_query", Desc: "run SQL over a csv file"},
		{Name: "pdf_extract", Desc: "extract text from a pdf"},
		{Name: "web_search", Desc: "search the web"},
	}
	if got := len(g.search("csv join")); got != 1 {
		t.Errorf("search(\"csv join\") returned %d, want 1 — terms must narrow", got)
	}
	if got := len(g.search("csv")); got != 2 {
		t.Errorf("search(\"csv\") returned %d, want 2", got)
	}
	// A core tool is already in front of the model; returning it wastes a turn.
	for _, h := range g.search("search") {
		if h.Name == "web_search" {
			t.Error("search returned a tool the model can already see")
		}
	}
}

func TestFindToolsUnlocksAndReports(t *testing.T) {
	g := newToolGate()
	g.all = []*schema.ToolInfo{
		{Name: "pdf_extract", Desc: "extract text from a pdf"},
		{Name: "csv_join", Desc: "join two csv files"},
	}
	ctx := withToolGate(context.Background(), g)
	out, err := findTools{}.Invoke(ctx, map[string]any{"query": "pdf"})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(out, "pdf_extract") {
		t.Fatalf("result does not name the tool: %s", out)
	}
	if !names(g.visible())["pdf_extract"] {
		t.Fatal("find_tools reported a tool it did not actually enable")
	}
}

// A miss must tell the model what DOES exist, or it will conclude the capability
// is missing and fall back to a shell workaround.
func TestFindToolsMissListsWhatExists(t *testing.T) {
	g := newToolGate()
	g.all = []*schema.ToolInfo{{Name: "csv_join", Desc: "join csv"}}
	ctx := withToolGate(context.Background(), g)
	out, _ := findTools{}.Invoke(ctx, map[string]any{"query": "quantum"})
	if !contains(out, "csv_join") {
		t.Fatalf("a miss did not list the available tools: %s", out)
	}
}

func TestFindToolsWithoutGateIsHarmless(t *testing.T) {
	if _, err := (findTools{}).Invoke(context.Background(), map[string]any{"query": "x"}); err != nil {
		t.Fatalf("find_tools errored with no gate installed: %v", err)
	}
}

// The budget guard strips every tool on the final turn to force an answer. The
// gate must not undo that.
func TestGateMiddlewareRespectsStrippedTools(t *testing.T) {
	g := newToolGate()
	g.remember(infos("web_search", "csv_join"))
	m := newGateMiddleware(g)
	st := &adk.ChatModelAgentState{ToolInfos: nil}
	_, out, err := m.BeforeModelRewriteState(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolInfos) != 0 {
		t.Fatal("gate handed tools back after the budget guard removed them")
	}
}

// Hiding a tool silently does not make the model ask for it — it makes the
// model improvise. The live check that motivated this: with qrcode hidden and
// no index, the agent went shell → python → pip install → python and tripped a
// danger-tool approval, instead of the single qrcode call it had made a minute
// before. find_tools must therefore ADVERTISE what it can unlock.
func TestGateAdvertisesHiddenToolsInFindTools(t *testing.T) {
	g := newToolGate()
	g.remember(infos("web_search", findToolsName, "qrcode", "pdf_extract"))
	var ft *schema.ToolInfo
	for _, ti := range g.visible() {
		if ti.Name == findToolsName {
			ft = ti
		}
	}
	if ft == nil {
		t.Fatal("find_tools is not visible; hidden tools would be unreachable")
	}
	for _, want := range []string{"qrcode", "pdf_extract"} {
		if !contains(ft.Desc, want) {
			t.Errorf("hidden tool %q is not advertised: %s", want, ft.Desc)
		}
	}
	if contains(ft.Desc, "web_search") {
		t.Error("a core tool is listed as needing to be unlocked")
	}
}

// The index must not corrupt the captured list, or the second turn would
// advertise a description instead of the real one.
func TestGateIndexDoesNotMutateCapturedList(t *testing.T) {
	g := newToolGate()
	g.remember(infos(findToolsName, "qrcode"))
	_ = g.visible()
	for _, ti := range g.all {
		if ti.Name == findToolsName && contains(ti.Desc, "qrcode") {
			t.Fatal("visible() mutated the captured tool list")
		}
	}
}

// With everything unlocked there is nothing to advertise, and the description
// must fall back to the plain one rather than dangling an empty list.
func TestGateNoIndexWhenNothingHidden(t *testing.T) {
	g := newToolGate()
	g.remember(infos("web_search", findToolsName))
	for _, ti := range g.visible() {
		if ti.Name == findToolsName && contains(ti.Desc, "Available on request:") {
			t.Fatal("advertised an empty index")
		}
	}
}

// The live miss that motivated the fallback: the agent searched "generate QR
// code image" and strict matching found nothing, because the tool's description
// never says "image". A miss costs a whole extra model round-trip.
func TestGateSearchFallsBackToPartialMatch(t *testing.T) {
	g := newToolGate()
	g.all = []*schema.ToolInfo{
		{Name: "qrcode", Desc: "Generate a QR-code PNG for a URL or text"},
		{Name: "csv_join", Desc: "join two csv files"},
	}
	hits := g.search("generate QR code image")
	if len(hits) == 0 || hits[0].Name != "qrcode" {
		t.Fatalf("partial match failed: %v", hits)
	}
	// An exact match must still take precedence over the fallback.
	if got := g.search("csv join"); len(got) != 1 || got[0].Name != "csv_join" {
		t.Fatalf("exact match regressed: %v", got)
	}
}

// A vague query must not unlock the whole catalogue — that would undo the gate.
func TestGateSearchFallbackIsBounded(t *testing.T) {
	g := newToolGate()
	for _, n := range []string{"a_file", "b_file", "c_file", "d_file", "e_file", "f_file"} {
		g.all = append(g.all, &schema.ToolInfo{Name: n, Desc: "works with a file somehow"})
	}
	if got := len(g.search("file zzz")); got > 4 {
		t.Fatalf("a vague query unlocked %d tools", got)
	}
}
