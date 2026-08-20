package api

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
)

const (
	testChartRel  = "query-charts/sales-20260818-190653-65b086/chart-01.svg"
	testReportRel = "monthly-20260630-025e4d09bb/index.html"
)

func seedSalesBIAssets(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		testChartRel:  `<svg xmlns="http://www.w3.org/2000/svg"></svg>`,
		testReportRel: `<!doctype html><title>report</title><script>void 0</script>`,
	} {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestResolveSalesBIAsset(t *testing.T) {
	root := seedSalesBIAssets(t)
	for _, rel := range []string{testChartRel, testReportRel} {
		got, err := resolveSalesBIAsset(root, rel)
		if err != nil {
			t.Fatalf("resolve %q: %v", rel, err)
		}
		if !strings.HasPrefix(got, root+string(filepath.Separator)) {
			t.Fatalf("resolved outside root: %s", got)
		}
	}

	invalid := []string{
		"../secret.html",
		`query-charts\sales-20260818-190653-65b086\chart-01.svg`,
		"query-charts/not-a-task/chart-01.svg",
		"query-charts/sales-20260818-190653-65b086/chart-01.html",
		"monthly-20260630-025e4d09bb/data.csv",
		"other/index.html",
	}
	for _, rel := range invalid {
		if got, err := resolveSalesBIAsset(root, rel); err == nil {
			t.Errorf("resolve %q unexpectedly succeeded: %s", rel, got)
		}
	}
}

func TestResolveSalesBIAssetRejectsSymlinkEscape(t *testing.T) {
	root := seedSalesBIAssets(t)
	outside := filepath.Join(t.TempDir(), "outside.html")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "monthly-20260630-025e4d09bb", "linked.html")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if got, err := resolveSalesBIAsset(root, "monthly-20260630-025e4d09bb/linked.html"); err == nil {
		t.Fatalf("symlink escape unexpectedly succeeded: %s", got)
	}
}

func TestSalesBIAssetResponsePolicy(t *testing.T) {
	root := seedSalesBIAssets(t)
	a := &API{SalesBIReportRoot: root}

	report := app.NewContext(0)
	report.Request.SetRequestURI("/sales-bi/asset?path=" + url.QueryEscape(testReportRel))
	a.SalesBIAsset(context.Background(), report)
	if report.Response.StatusCode() != 200 {
		t.Fatalf("report status = %d, body = %s", report.Response.StatusCode(), report.Response.Body())
	}
	if got := string(report.Response.Header.Peek("Content-Type")); got != "text/html; charset=utf-8" {
		t.Errorf("content type = %q", got)
	}
	if got := string(report.Response.Header.Peek("Content-Disposition")); !strings.HasPrefix(got, "inline") {
		t.Errorf("content disposition = %q", got)
	}
	if got := string(report.Response.Header.Peek("Content-Security-Policy")); !strings.Contains(got, "sandbox allow-scripts") || !strings.Contains(got, "default-src 'none'") {
		t.Errorf("CSP = %q", got)
	}
	if got := string(report.Response.Header.Peek("X-Content-Type-Options")); got != "nosniff" {
		t.Errorf("nosniff = %q", got)
	}

	download := app.NewContext(0)
	download.Request.SetRequestURI("/sales-bi/asset?path=" + url.QueryEscape(testReportRel) + "&download=1")
	a.SalesBIAsset(context.Background(), download)
	if got := string(download.Response.Header.Peek("Content-Disposition")); !strings.HasPrefix(got, "attachment") {
		t.Errorf("download content disposition = %q", got)
	}

	missing := app.NewContext(0)
	missing.Request.SetRequestURI("/sales-bi/asset?path=" + url.QueryEscape("../secret.html"))
	a.SalesBIAsset(context.Background(), missing)
	if missing.Response.StatusCode() != 404 {
		t.Errorf("invalid path status = %d", missing.Response.StatusCode())
	}
}
