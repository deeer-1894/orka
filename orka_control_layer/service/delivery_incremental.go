package service

import (
	"context"
	"os"
	"path"
	"strings"

	"github.com/orka-oss/orka_core/artifacts"
)

const (
	automaticFileBytes  = 256 << 10
	automaticBatchBytes = 2 << 20
	automaticBatchFiles = 16
)

type artifactRevision struct {
	size     int64
	modified int64
	mode     os.FileMode
}

func revision(info os.FileInfo) artifactRevision {
	return artifactRevision{info.Size(), info.ModTime().UnixNano(), info.Mode()}
}

// inspectProduced adds early structural feedback, not a delivery verdict. Only
// declared small files changed since the last inspection are read. Large files,
// archives, dependencies and missing requirements remain check_delivery's job.
// Stat revisions are an optimization, never the final integrity authority.
func (d *deliveryTracker) inspectProduced(ctx context.Context, tool string) string {
	if d == nil || ctx.Err() != nil {
		return ""
	}
	switch tool {
	case "file_write", "file_patch", "file_edit", "shell", "python":
	default:
		return ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	root, err := os.OpenRoot(d.root)
	if err != nil {
		return ""
	}
	defer root.Close()
	if d.inspected == nil {
		d.inspected = map[string]artifactRevision{}
	}
	var selected []string
	revisions := map[string]artifactRevision{}
	var bytes int64
	for _, p := range d.outputs {
		if ctx.Err() != nil {
			return ""
		}
		switch strings.ToLower(path.Ext(p)) {
		case ".json", ".csv", ".svg", ".html", ".htm":
		default:
			continue
		}
		info, err := root.Stat(p)
		if err != nil || !info.Mode().IsRegular() || info.Size() > automaticFileBytes {
			continue
		}
		rev := revision(info)
		if last, ok := d.inspected[p]; ok && last == rev {
			continue
		}
		if len(selected) >= automaticBatchFiles || bytes+info.Size() > automaticBatchBytes {
			continue
		}
		selected = append(selected, p)
		revisions[p] = rev
		bytes += info.Size()
	}
	if len(selected) == 0 {
		return ""
	}
	var failures []string
	visibleBytes := 0
	for _, p := range selected {
		report := artifacts.Check(ctx, d.root, []string{p})
		if ctx.Err() != nil {
			return ""
		}
		if len(report.Failures) > 0 {
			message := strings.Join(report.Failures, "\n")
			// Defer diagnostics that do not fit. Never cache an unseen failure.
			if visibleBytes > 0 && visibleBytes+len(message)+1 > 2400 {
				break
			}
			message = trunc(message, 2400)
			failures = append(failures, message)
			visibleBytes += len(message) + 1
		}
		// Do not remember a revision changed by a concurrent writer while read.
		if info, err := root.Stat(p); err == nil && revision(info) == revisions[p] {
			d.inspected[p] = revisions[p]
		}
	}
	if len(failures) == 0 {
		return ""
	}
	return "\n[Produced file structure issues]\n" + strings.Join(failures, "\n") + "\nRepair these files or finish their local dependencies before delivery. These checks do not establish business correctness; run task-specific assertions as well."
}
