package db

import "fmt"

// Capabilities a task's unattended runs may be granted. Searching and reading
// the public web is the default; everything else is opt-in per task, because a
// triggered run has nobody watching to approve it and its context may carry
// content an outsider wrote (a webhook payload, a fetched page).
//
// A run may READ private data or REACH the open network, never both: with
// both, injected text can have the model read a file and send it to an
// attacker's URL. Reading the workspace and running code (which can read it)
// therefore withhold the network tools, and cannot be combined with browser.
const (
	CapWorkspaceWrite = "workspace_write" // create and change files in the task's own workspace
	CapWorkspaceRead  = "workspace_read"  // read the task's workspace files (no network then)
	CapCode           = "code"            // shell and python (reads the workspace: no network then)
	CapBrowser        = "browser"         // drive the DOM browser (network)
)

var taskCapabilities = map[string]bool{CapWorkspaceWrite: true, CapWorkspaceRead: true, CapCode: true, CapBrowser: true}

// ReadsPrivate reports whether the policy grants a private-data read, which
// takes the network away.
func (p TaskToolPolicy) ReadsPrivate() bool {
	for _, c := range p.Allow {
		if c == CapWorkspaceRead || c == CapCode {
			return true
		}
	}
	return false
}

// TaskToolPolicy declares what an unattended run of a task may do. An empty
// Allow is read-only.
type TaskToolPolicy struct {
	Allow []string `bson:"allow" json:"allow"`
}

// DefaultTaskToolPolicy is what a task gets when it never chose: read plus
// writing its own workspace, enough to produce a report and nothing more.
func DefaultTaskToolPolicy() TaskToolPolicy {
	return TaskToolPolicy{Allow: []string{CapWorkspaceWrite}}
}

// Normalize rejects unknown capabilities and drops duplicates.
func (p TaskToolPolicy) Normalize() (TaskToolPolicy, error) {
	out := TaskToolPolicy{Allow: []string{}}
	seen := map[string]bool{}
	for _, c := range p.Allow {
		if !taskCapabilities[c] {
			return p, fmt.Errorf("未知的权限：%s", c)
		}
		if !seen[c] {
			seen[c] = true
			out.Allow = append(out.Allow, c)
		}
	}
	if seen[CapBrowser] && (seen[CapWorkspaceRead] || seen[CapCode]) {
		return p, fmt.Errorf("“操作浏览器”需要联网，不能和“读取工作区文件”或“执行代码”同时开启：同时具备读取私有数据和访问外网，会被网页或事件里的恶意内容诱导泄露数据")
	}
	return out, nil
}
