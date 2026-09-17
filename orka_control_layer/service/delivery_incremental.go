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
// dependencies and missing requirements remain check_delivery's job. Small ZIPs
// also receive advisory document-reference review, without enforcing prose as a contract.
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
	return d.inspectFiles(ctx, d.snapshot())
}

// Runner receipts are observations of actual writes, not delivery declarations.
// Reuse the same revision cache so a declared ZIP is not reviewed twice.
func (d *deliveryTracker) inspectWrittenArchives(ctx context.Context, paths []string) string {
	var archives []string
	for _, p := range paths {
		if artifacts.ValidPath(p) && strings.EqualFold(path.Ext(p), ".zip") {
			archives = append(archives, p)
			if len(archives) == artifacts.MaxFiles {
				break
			}
		}
	}
	return d.inspectFiles(ctx, archives)
}

func (d *deliveryTracker) inspectFiles(ctx context.Context, paths []string) string {
	if d == nil || len(paths) == 0 || ctx.Err() != nil {
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
	for _, p := range paths {
		if ctx.Err() != nil {
			return ""
		}
		switch strings.ToLower(path.Ext(p)) {
		case ".json", ".csv", ".svg", ".html", ".htm", ".zip":
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
		issues := append([]string(nil), report.Failures...)
		for _, warning := range report.Warnings {
			issues = append(issues, "Review (not a failed structural check): "+warning)
		}
		if len(issues) > 0 {
			message := strings.Join(issues, "\n")
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
	return "\n[Produced file structure issues and review notes]\n" + strings.Join(failures, "\n") + "\nRepair structural failures. Review advisory references against the actual request; do not create files merely because documentation describes an example. These checks do not establish business correctness."
}
