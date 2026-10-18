package artifacts

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func archiveFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestArchiveReportsMissingDocumentedEvidence(t *testing.T) {
	root := t.TempDir()
	data := archiveFixture(t, map[string]string{
		"project/README.md":          "真实执行日志：`tests/test_log_batch.txt`。\n[验证记录](validation.md)\n",
		"project/tests/test_old.log": "Ran 26 tests\nOK\n",
	})
	if err := os.WriteFile(filepath.Join(root, "release.zip"), data, 0600); err != nil {
		t.Fatal(err)
	}
	report := Check(context.Background(), root, []string{"release.zip"})
	b, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tests/test_log_batch.txt", "validation.md", "warnings"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing archive reference diagnostic %q: %s", want, b)
		}
	}
	if !report.OK {
		t.Fatal("ambiguous document references must not become a false structural failure")
	}
}

func TestArchiveReferencesUseOnlyPackagedNamespace(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "test.log"), []byte("OK"), 0600); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"README.md": "日志：[实际日志](test.log)。"}
	check := func() Report {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "release.zip"), archiveFixture(t, files), 0600); err != nil {
			t.Fatal(err)
		}
		return Check(context.Background(), root, []string{"release.zip"})
	}
	if r := check(); len(r.Warnings) != 1 || !r.OK {
		t.Fatalf("workspace masked archive omission: %+v", r)
	}
	files["test.log"] = "Ran 52 tests\nOK\n"
	if r := check(); !r.OK || len(r.Warnings) != 0 {
		t.Fatalf("packaged evidence not recognized: %+v", r)
	}
}

func TestArchiveReviewIgnoresExamplesAndExistingReferences(t *testing.T) {
	data := archiveFixture(t, map[string]string{
		"project/docs/README.md":   "[日志](../tests/result.log#last)\n[外部](https://example.org/missing.md)\n`python make.py --out future.csv`\n```sh\n[示例](missing.txt)\n`missing.log`\n```\n    `indented.log`\n",
		"project/tests/result.log": "OK",
	})
	if got := reviewArchive(context.Background(), "release.zip", data); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestArchiveReviewBoundsAndNestedArchivesRemainAdvisory(t *testing.T) {
	data := archiveFixture(t, map[string]string{
		"a.md":    strings.Repeat("x", archiveReviewDocumentBytes+1),
		"b.md":    "[missing](missing.log)",
		"old.zip": "old archive may be intentional",
	})
	got := strings.Join(reviewArchive(context.Background(), "release.zip", data), "\n")
	for _, want := range []string{"nested archive", "missing.log", "incomplete"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q: %s", want, got)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := strings.Join(reviewArchive(ctx, "release.zip", data), " "); !strings.Contains(got, "incomplete") {
		t.Fatal(got)
	}
}

func TestDocumentFileReferencesAreConservative(t *testing.T) {
	text := "[encoded](logs/%E6%97%A5%E5%BF%97.log?download=1#end) `tests/a.txt` [remote](//example.org/x.md) `https://example.org/z.log` `/tmp/generated.log` `*.csv`\n~~~~\n`example.log`\n~~~\n`still-example.log`\n~~~~\n[actual](actual.md)"
	got := documentFileReferences(text)
	want := []string{"logs/日志.log", "tests/a.txt", "actual.md"}
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Fatalf("got %s want %s", a, b)
	}
}
