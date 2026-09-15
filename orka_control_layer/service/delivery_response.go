package service

import (
	"context"
	"net/url"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/orka-oss/orka_core/artifacts"
)

// Explicit file-receipt delivery has one source of substantive conclusions: the actual report.
// Do not generate a second statistical narrative after checking the artifacts.
// This receipt reports structural checks only; it is not a semantic verifier.
// Ordinary answers, unfinished work and tool progress retain their content.
func deliveryResponse(ctx context.Context, m *schema.Message) *schema.Message {
	d := deliveryFrom(ctx)
	if m == nil || m.Role != schema.Assistant || len(m.ToolCalls) != 0 || d == nil || d.responseMode() != "file_receipt" || ctx.Err() != nil || budgetFrom(ctx).exhausted() != "" {
		return m
	}
	paths := d.snapshot()
	p := planTrackerFrom(ctx)
	if len(paths) == 0 || len(p.snapshot()) == 0 || len(p.unfinished()) != 0 {
		return m
	}
	report := artifacts.CheckDeclared(ctx, d.root, paths)
	if !report.OK || ctx.Err() != nil || budgetFrom(ctx).exhausted() != "" {
		return m
	}
	var text strings.Builder
	text.WriteString("已生成以下交付文件，分析结论与具体数字请查看报告和数据文件：\n\n")
	for _, f := range report.Files {
		// Escape each segment: filenames may contain Markdown metacharacters,
		// spaces, Unicode or URL query/fragment delimiters.
		parts := strings.Split(f.Path, "/")
		for i := range parts {
			parts[i] = url.PathEscape(parts[i])
		}
		label := strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "\n", " ", "\r", " ").Replace(f.Path)
		text.WriteString("- [" + label + "](./" + strings.Join(parts, "/") + ")\n")
	}
	text.WriteString("\n验收结果：以上文件通过非空及适用的格式、资源引用检查。业务规则、数据结论和引用依据的验证结果及限制，请查看报告；文件检查不代表内容已全部核实。")
	copy := *m
	copy.Content = text.String()
	return &copy
}

// Content deltas cannot be retracted once sent. Hold only the main agent's
// content for explicitly selected file receipts until calls identify progress vs final.
// Reasoning and tool events still stream; ordinary chat keeps token streaming.
func bufferDeliveryContent(ctx context.Context, agentID string) bool {
	return agentID == "" && deliveryFrom(ctx).responseMode() == "file_receipt" && len(deliveryFrom(ctx).snapshot()) > 0
}
