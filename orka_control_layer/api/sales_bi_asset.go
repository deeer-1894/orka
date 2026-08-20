package api

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

const maxSalesBIAssetSize = 25 << 20

var (
	salesBIChartTaskRE = regexp.MustCompile(`^sales-\d{8}-\d{6}-[0-9a-f]{6}$`)
	salesBIChartFileRE = regexp.MustCompile(`^chart-\d{2}\.svg$`)
	salesBIReportDirRE = regexp.MustCompile(`^(daily|weekly|monthly)-\d{8}-[0-9a-f]{8,16}$`)
	salesBIAssetExts   = map[string]bool{
		".css": true, ".gif": true, ".htm": true, ".html": true,
		".jpeg": true, ".jpg": true, ".js": true, ".json": true,
		".png": true, ".svg": true, ".webp": true,
		".woff": true, ".woff2": true,
	}
)

// SalesBIAsset serves generated BI files through Orka's authenticated origin.
func (a *API) SalesBIAsset(ctx context.Context, c *app.RequestContext) {
	_ = ctx
	assetPath, err := resolveSalesBIAsset(a.SalesBIReportRoot, string(c.Query("path")))
	if err != nil {
		fail(c, consts.StatusNotFound, "not found")
		return
	}
	data, err := readSalesBIAsset(assetPath)
	if err != nil {
		fail(c, consts.StatusNotFound, "not found")
		return
	}
	serveSalesBIAsset(c, assetPath, data)
}

func readSalesBIAsset(assetPath string) ([]byte, error) {
	info, err := os.Stat(assetPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSalesBIAssetSize {
		return nil, errors.New("invalid sales BI asset")
	}
	return os.ReadFile(assetPath)
}

func serveSalesBIAsset(c *app.RequestContext, assetPath string, data []byte) {
	ext := strings.ToLower(filepath.Ext(assetPath))
	contentType := mime.TypeByExtension(ext)
	switch ext {
	case ".html", ".htm":
		contentType = "text/html; charset=utf-8"
		// The report can run self-contained interactions but has an opaque origin
		// and cannot access Orka storage or make network requests.
		c.Response.Header.Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; img-src data:; font-src data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
	case ".svg":
		contentType = "image/svg+xml; charset=utf-8"
		c.Response.Header.Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	disposition := "inline"
	if string(c.Query("download")) == "1" {
		disposition = "attachment"
	}
	c.Response.Header.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": filepath.Base(assetPath)}))
	c.Response.Header.Set("Cache-Control", "private, max-age=300")
	c.Response.Header.Set("Referrer-Policy", "no-referrer")
	c.Response.Header.Set("X-Content-Type-Options", "nosniff")
	c.Data(consts.StatusOK, contentType, data)
}

func resolveSalesBIAsset(root, relative string) (string, error) {
	if root == "" || relative == "" || strings.ContainsRune(relative, '\x00') || strings.Contains(relative, `\`) {
		return "", errors.New("invalid sales BI asset path")
	}
	relative = strings.TrimLeft(relative, "/")
	clean := path.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("sales BI asset path escaped root")
	}
	parts := strings.Split(clean, "/")
	if parts[0] == "query-charts" {
		if len(parts) != 3 || !salesBIChartTaskRE.MatchString(parts[1]) || !salesBIChartFileRE.MatchString(parts[2]) {
			return "", errors.New("invalid sales BI chart path")
		}
	} else if len(parts) < 2 || !salesBIReportDirRE.MatchString(parts[0]) {
		return "", errors.New("invalid sales BI report path")
	}
	if !salesBIAssetExts[strings.ToLower(filepath.Ext(parts[len(parts)-1]))] {
		return "", errors.New("unsupported sales BI asset type")
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve sales BI root: %w", err)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("resolve sales BI root links: %w", err)
	}
	targetReal, err := filepath.EvalSymlinks(filepath.Join(rootReal, filepath.FromSlash(clean)))
	if err != nil {
		return "", fmt.Errorf("resolve sales BI asset links: %w", err)
	}
	rel, err := filepath.Rel(rootReal, targetReal)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("sales BI asset resolved outside root")
	}
	return targetReal, nil
}
