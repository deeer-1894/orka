// Package artifacts performs bounded, read-only structural delivery checks.
// Passing checks do not establish business correctness or citation support.
package artifacts

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

const maxFileBytes = 32 << 20
const maxArchiveBytes = 128 << 20
const MaxFiles = 128

type File struct {
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type Report struct {
	OK       bool     `json:"ok"`
	Files    []File   `json:"files"`
	Failures []string `json:"failures"`
}

// ValidPath accepts portable workspace-relative paths. os.Root additionally
// confines symlinks at the actual read boundary.
func ValidPath(p string) bool {
	return p != "" && p != "." && !strings.ContainsAny(p, "\\\x00") && !strings.HasPrefix(p, "/") && path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../")
}
func Check(ctx context.Context, rootPath string, paths []string) Report {
	report := Report{OK: true, Files: []File{}, Failures: []string{}}
	fail := func(p string, err error) {
		report.OK = false
		report.Failures = append(report.Failures, p+": "+err.Error())
	}
	if len(paths) == 0 || len(paths) > MaxFiles {
		fail("delivery", fmt.Errorf("declare 1..%d files", MaxFiles))
		return report
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		fail("workspace", err)
		return report
	}
	defer root.Close()
	seen := map[string]bool{}
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			fail(p, err)
			break
		}
		if !ValidPath(p) {
			fail(p, fmt.Errorf("invalid workspace-relative path"))
			continue
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		data, err := read(root, p)
		if err != nil {
			fail(p, err)
			continue
		}
		if err = validate(ctx, root, p, data); err != nil {
			fail(p, err)
			continue
		}
		digest := sha256.Sum256(data)
		report.Files = append(report.Files, File{Path: p, Bytes: len(data), SHA256: hex.EncodeToString(digest[:])})
	}
	return report
}
func read(root *os.Root, p string) ([]byte, error) {
	st, err := root.Stat(p)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() == 0 || st.Size() > maxFileBytes {
		return nil, fmt.Errorf("requires a nonempty regular file of at most %d bytes", maxFileBytes)
	}
	f, err := root.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err = f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) == 0 || len(b) > maxFileBytes {
		return nil, fmt.Errorf("empty or oversized file")
	}
	return b, nil
}
func validate(ctx context.Context, root *os.Root, p string, b []byte) error {
	switch strings.ToLower(path.Ext(p)) {
	case ".json":
		if !json.Valid(b) {
			return fmt.Errorf("invalid JSON")
		}
	case ".csv":
		r := csv.NewReader(bytes.NewReader(b))
		header, err := r.Read()
		if err != nil {
			return err
		}
		names := map[string]bool{}
		for _, name := range header {
			name = strings.TrimSpace(name)
			if name == "" || names[name] {
				return fmt.Errorf("empty or duplicate CSV column %q", name)
			}
			names[name] = true
		}
		for {
			_, err = r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if err = ctx.Err(); err != nil {
				return err
			}
		}
	case ".svg":
		d := xml.NewDecoder(bytes.NewReader(b))
		depth, roots := 0, 0
		for {
			tok, err := d.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				for _, attr := range t.Attr {
					if (attr.Name.Local == "width" || attr.Name.Local == "height") && strings.Contains(attr.Value, "%%") {
						return fmt.Errorf("invalid SVG percentage length %s=%q", attr.Name.Local, attr.Value)
					}
				}
				if depth == 0 {
					roots++
					if t.Name.Local != "svg" || roots != 1 {
						return fmt.Errorf("expected one SVG root")
					}
				}
				depth++
			case xml.EndElement:
				depth--
			case xml.CharData:
				if depth == 0 && strings.TrimSpace(string(t)) != "" {
					return fmt.Errorf("text outside SVG root")
				}
			}
		}
		if roots != 1 {
			return fmt.Errorf("missing SVG root")
		}
	case ".html", ".htm":
		return checkHTML(root, p, b)
	case ".zip":
		return checkZIP(ctx, b)
	}
	return nil
}
func checkZIP(ctx context.Context, b []byte) error {
	r, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return err
	}
	if len(r.File) == 0 || len(r.File) > 4096 {
		return fmt.Errorf("empty archive or too many entries")
	}
	seen := map[string]bool{}
	remaining := int64(maxArchiveBytes)
	for _, f := range r.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := strings.TrimSuffix(f.Name, "/")
		if !ValidPath(name) || seen[name] || f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe/duplicate archive entry %q", f.Name)
		}
		seen[name] = true
		if f.FileInfo().IsDir() {
			continue
		}
		if f.UncompressedSize64 > uint64(remaining) {
			return fmt.Errorf("archive exceeds expanded size limit")
		}
		in, err := f.Open()
		if err != nil {
			return err
		}
		n, err := io.Copy(io.Discard, io.LimitReader(in, remaining+1))
		in.Close()
		if err != nil {
			return err
		}
		remaining -= n
		if remaining < 0 {
			return fmt.Errorf("archive exceeds expanded size limit")
		}
	}
	return nil
}
