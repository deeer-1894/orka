package artifacts

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReviewBrokenSrcset(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "report.html"), []byte(`<picture><source srcset="missing.svg"><img alt="plot"></picture>`), 0600)
	if got := Check(context.Background(), root, []string{"report.html"}); got.OK {
		t.Fatalf("missing image source accepted: %+v", got)
	}
}
