package service

import "github.com/orka-oss/orka_control_layer/db"

// Unattended task runs — started by a schedule, a watched condition or an
// incoming webhook — have nobody to answer an approval, and their context can
// hold text an outsider wrote (a webhook payload, a fetched page). So what they
// may do is not left to the prompt: the task's declared policy becomes the
// run's tool whitelist, and filterToolsForRequest drops everything else before
// the model ever sees it.

// unattendedTriggers are the run origins held to the task's tool policy.
var unattendedTriggers = map[string]bool{"schedule": true, "condition": true, "webhook": true}

func unattendedRun(trigger string) bool { return unattendedTriggers[trigger] }

// readOnlyTools are always available unattended: they read the workspace or
// the web, or compute, and change nothing.
var readOnlyTools = []string{
	"file_read", "file_list",
	"web_search", "fetch_url", "discover_docs", "read_section", "search_evidence", "weather",
	"current_time", "calculator", "unit_convert", "base64", "hash", "uuid", "json_format",
	"text_stats", "regex_extract", "json_query", "datetime", "random",
	"currency", "timezone", "csv_query", "csv_stats", "csv_to_json", "pdf_extract", "doc_read", "sql_query",
	"find_skills", "apply_skill", "artifact_get",
}

// capabilityTools lists the tools each opt-in capability adds. Anything named
// in neither list — http_request, run_agent, memory, skill installation,
// ingest_factor, every external connector, and any tool added later — is
// unavailable to unattended runs until it is classified here.
var capabilityTools = map[string][]string{
	db.CapWorkspaceWrite: {
		"file_write", "file_edit", "file_patch", "file_delete",
		"render_report", "doc_export", "chart", "xlsx_to_csv", "csv_to_xlsx", "csv_join", "slides", "qrcode",
		"artifact_publish",
		"backtest", "gp_evolve", "validate_factor", "factor_agreement", "recall_similar_factors", "weight_portfolio",
	},
	db.CapCode:    {"shell", "python"},
	db.CapBrowser: {"browser"},
}

// noTools keeps a whitelist non-empty when nothing is allowed: an empty
// EnabledTools means "everything".
const noTools = "\x00none"

// policyTools is the explicit tool whitelist a policy grants.
func policyTools(p db.TaskToolPolicy) []string {
	out := append([]string(nil), readOnlyTools...)
	for _, c := range p.Allow {
		out = append(out, capabilityTools[c]...)
	}
	return out
}

// applyTaskToolPolicy narrows an unattended request to its task's policy (the
// default when the trigger did not carry one). A caller's own whitelist can
// narrow it further but never widen it.
func applyTaskToolPolicy(req *ChatRunRequest) {
	if !unattendedRun(req.Trigger) {
		return
	}
	policy := db.DefaultTaskToolPolicy()
	if req.ToolPolicy != nil {
		policy = *req.ToolPolicy
	}
	allowed := policyTools(policy)
	if len(req.EnabledTools) > 0 {
		declared := allowed[:0:0]
		for _, name := range allowed {
			if enabledNames(req.EnabledTools, name) {
				declared = append(declared, name)
			}
		}
		allowed = declared
	}
	if len(allowed) == 0 {
		allowed = []string{noTools}
	}
	req.EnabledTools = allowed
}

// enabledNames mirrors filterEnabled's matching for a tool known by name.
func enabledNames(enabled []string, name string) bool {
	group := groupForName(name)
	for _, e := range enabled {
		if e == name || (group != "" && e == group) || (e == "search" && group == "web") {
			return true
		}
	}
	return false
}
