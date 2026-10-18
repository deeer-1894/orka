package artifacts

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckDetectsBrokenDelivery(t *testing.T) {
	root := t.TempDir()
	fixtures := map[string]string{
		"good.csv": "name,value\na,1\n", "duplicate.csv": "name,name\na,b\n", "ragged.csv": "a,b\n1\n",
		"valid.json": "{\"n\":1}", "broken.json": "{", "empty.md": "",
		"chart.svg":    "<svg xmlns=\"http://www.w3.org/2000/svg\"><rect width=\"1\"/></svg>",
		"broken.svg":   "<svg><rect></svg>",
		"good.html":    "<html><img src=\"chart.svg\"></html>",
		"missing.html": "<img src=\"missing.svg\">", "broken.html": "<img src=\"<svg xmlns=\"http://www.w3.org/2000/svg\">\">",
	}
	for p, data := range fixtures {
		if err := os.WriteFile(filepath.Join(root, p), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	good := Check(context.Background(), root, []string{"good.csv", "valid.json", "chart.svg", "good.html"})
	if !good.OK {
		t.Fatalf("valid files rejected: %+v", good)
	}
	for _, p := range []string{"missing.md", "empty.md", "duplicate.csv", "ragged.csv", "broken.json", "broken.svg", "missing.html", "broken.html"} {
		t.Run(p, func(t *testing.T) {
			r := Check(context.Background(), root, []string{p})
			if r.OK || len(r.Failures) == 0 {
				t.Fatalf("broken delivery accepted: %+v", r)
			}
		})
	}
	if err := os.WriteFile(filepath.Join(root, "valid.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if Check(context.Background(), root, []string{"valid.json"}).OK {
		t.Fatal("stale success survived file mutation")
	}
}

func TestCheckConfinesReadsAndBoundsArchives(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"escape", "../secret", filepath.Join(outside, "secret")} {
		if Check(context.Background(), root, []string{p}).OK {
			t.Fatalf("accepted escape %q", p)
		}
	}
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, _ := w.Create("../escape")
	f.Write([]byte("bad"))
	w.Close()
	os.WriteFile(filepath.Join(root, "unsafe.zip"), b.Bytes(), 0600)
	if Check(context.Background(), root, []string{"unsafe.zip"}).OK {
		t.Fatal("accepted traversal archive")
	}
	os.WriteFile(filepath.Join(root, "corrupt.zip"), []byte("not a zip"), 0600)
	if Check(context.Background(), root, []string{"corrupt.zip"}).OK {
		t.Fatal("accepted corrupt archive")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Check(ctx, root, []string{"unsafe.zip"}).OK {
		t.Fatal("ignored cancellation")
	}
}

func TestSVGRejectsMalformedPercentageLengths(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"invalid.svg", `<svg><rect width="100%%" height="100%%"/></svg>`, false},
		{"valid.svg", `<svg width="100%" height="20"><text>100%% is a label</text></svg>`, true},
	} {
		if err := os.WriteFile(filepath.Join(root, tc.name), []byte(tc.body), 0600); err != nil {
			t.Fatal(err)
		}
		if got := Check(context.Background(), root, []string{tc.name}); got.OK != tc.valid {
			t.Fatalf("%s: %+v", tc.name, got)
		}
	}
}
