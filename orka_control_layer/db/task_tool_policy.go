package db

import "fmt"

// Capabilities a task's unattended runs may be granted beyond reading. Reading
// files and the web is always allowed; everything with a side effect is opt-in
// per task, because a triggered run has nobody watching to approve it and its
// context may carry content an outsider wrote.
const (
	CapWorkspaceWrite = "workspace_write" // create and change files in the task's own workspace
	CapCode           = "code"            // shell and python
	CapBrowser        = "browser"         // drive the DOM browser
)

var taskCapabilities = map[string]bool{CapWorkspaceWrite: true, CapCode: true, CapBrowser: true}

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
	return out, nil
}
