package artifacts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeliveryRejectsReportThatContradictsBoundCSV(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"summary.csv":         "channel,orders\napp,18\nweb,19\n",
		"summary.report.json": `{"kind":"orka.report/v1","output":"report.md","template":"每组订单数 {{minimum}}～{{maximum}}。","bindings":{"minimum":{"csv":"summary.csv","column":"orders","op":"min"},"maximum":{"csv":"summary.csv","column":"orders","op":"max"}}}`,
		"report.md":           "每组订单数 17～19。",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func() Report { return Check(context.Background(), root, []string{"report.md", "summary.report.json"}) }
	if r := check(); r.OK {
		t.Fatal("structurally valid report with wrong bound range passed delivery")
	}
	if err := os.WriteFile(filepath.Join(root, "report.md"), []byte("每组订单数 18～19。"), 0600); err != nil {
		t.Fatal(err)
	}
	if r := check(); !r.OK {
		t.Fatal(r.Failures)
	}
	if err := os.WriteFile(filepath.Join(root, "summary.csv"), []byte("channel,orders\napp,20\nweb,19\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if r := check(); r.OK || !strings.Contains(strings.Join(r.Failures, " "), "stale") {
		t.Fatalf("changed CSV did not invalidate report: %+v", r)
	}
}
