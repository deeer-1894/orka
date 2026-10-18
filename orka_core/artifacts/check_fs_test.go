package artifacts

import (
	"context"
	"testing"
	"testing/fstest"
)

func TestCheckFSUsesProvidedFilesystem(t *testing.T) {
	files := fstest.MapFS{
		"out/index.html": {Data: []byte(`<link href="style.css" rel="stylesheet"><img src="chart.svg">`)},
		"out/style.css":  {Data: []byte(`body { color: red; }`)},
		"out/chart.svg":  {Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)},
		"out/data.json":  {Data: []byte(`{"value":42}`)},
	}
	paths := []string{"out/index.html", "out/style.css", "out/chart.svg", "out/data.json"}
	if r := CheckFS(context.Background(), files, paths); !r.OK || len(r.Files) != 4 {
		t.Fatalf("valid snapshot: %+v", r)
	}
	delete(files, "out/style.css")
	if r := CheckFS(context.Background(), files, paths[:1]); r.OK {
		t.Fatal("missing reference passed")
	}
	files["out/data.json"].Data = []byte(`{broken`)
	if r := CheckFS(context.Background(), files, paths[3:]); r.OK {
		t.Fatal("invalid JSON passed")
	}
	if r := CheckFS(context.Background(), files, []string{"../escape"}); r.OK {
		t.Fatal("path escape passed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := CheckFS(ctx, files, paths); r.OK {
		t.Fatal("cancellation passed")
	}
}
