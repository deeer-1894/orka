package artifacts

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

const (
	archiveReviewDocuments     = 32
	archiveReviewDocumentBytes = 256 << 10
	archiveReviewTotalBytes    = 2 << 20
	archiveReviewWarnings      = 20
)

// reviewArchive is advisory: documentation may describe future outputs or
// examples. Never promote these references into mandatory delivery requirements.
// Only archive entries are consulted, so a live workspace cannot mask omissions.
func reviewArchive(ctx context.Context, filename string, data []byte) []string {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil
	} // Integrity validation reports malformed ZIPs.
	entries := make(map[string]*zip.File, len(r.File))
	var docs, nested []string
	for _, f := range r.File {
		entries[f.Name] = f
		if f.FileInfo().IsDir() {
			continue
		}
		switch strings.ToLower(path.Ext(f.Name)) {
		case ".md", ".markdown":
			docs = append(docs, f.Name)
		case ".zip":
			nested = append(nested, f.Name)
		}
	}
	sort.Strings(docs)
	sort.Strings(nested)
	var warnings []string
	seen := map[string]bool{}
	add := func(text string) {
		if !seen[text] {
			seen[text] = true
			warnings = append(warnings, filename+": "+text)
		}
	}
	for _, name := range nested {
		if len(warnings) >= archiveReviewWarnings {
			break
		}
		add(fmt.Sprintf("nested archive %q; confirm it is intended, not a stale release", name))
	}
	remaining, inspected, incomplete := archiveReviewTotalBytes, 0, false
	for _, name := range docs {
		if ctx.Err() != nil || inspected >= archiveReviewDocuments || len(warnings) >= archiveReviewWarnings {
			incomplete = true
			break
		}
		f := entries[name]
		if f.UncompressedSize64 > uint64(min(archiveReviewDocumentBytes, remaining)) {
			incomplete = true
			continue
		}
		in, err := f.Open()
		if err != nil {
			incomplete = true
			continue
		}
		b, err := io.ReadAll(io.LimitReader(in, int64(min(archiveReviewDocumentBytes, remaining))+1))
		in.Close()
		if err != nil || len(b) > remaining || len(b) > archiveReviewDocumentBytes {
			incomplete = true
			continue
		}
		inspected++
		remaining -= len(b)
		for _, ref := range documentFileReferences(string(b)) {
			if len(warnings) >= archiveReviewWarnings {
				incomplete = true
				break
			}
			if path.Base(ref) == path.Base(filename) && !strings.Contains(ref, "/") {
				continue // The enclosing release cannot contain its own final bytes.
			}
			target := path.Join(path.Dir(name), ref)
			entry := entries[target]
			if !ValidPath(target) || entry == nil || entry.FileInfo().IsDir() {
				add(fmt.Sprintf("document %q references %q, absent from this archive; include it if promised, or clarify that it is an example/generated later", name, ref))
			}
		}
	}
	if len(nested) > archiveReviewWarnings {
		incomplete = true
	}
	if incomplete {
		warnings = append(warnings, filename+": archive reference review incomplete (bounded document scan); no claim that all references were checked")
	}
	return warnings
}
