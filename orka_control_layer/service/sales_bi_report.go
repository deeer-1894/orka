package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/orka-oss/orka_control_layer/service/middlewares"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

func pendingReportID(raw string) string {
	var result struct {
		Status   string `json:"status"`
		Action   string `json:"action"`
		ReportID string `json:"report_id"`
	}
	if json.Unmarshal([]byte(raw), &result) != nil || result.Status != "ok" || result.Action != "prepare" {
		return ""
	}
	return result.ReportID
}

func publishPendingSalesBIReport(ctx context.Context, rc *agent.RunContext, tools []agent.BaseTool) error {
	reportID := pendingReportID(rc.Str(salesBIReportPendingKey))
	if reportID == "" {
		return errors.New("Sales BI report continuation has no prepared report_id")
	}
	var report agent.BaseTool
	for _, candidate := range tools {
		if candidate.Name() == "sales_report_generate" {
			report = candidate
			break
		}
	}
	if report == nil {
		return errors.New("Sales BI report continuation requires sales_report_generate")
	}
	args := map[string]any{
		"action":    "publish",
		"report_id": reportID,
		"narrative": map[string]any{},
	}
	result, err := report.Invoke(ctx, args)
	if err != nil {
		return err
	}
	middlewares.AddRunTools(rc, 1)
	rc.Emit(messages.Tool("call", map[string]any{
		"tool":   report.Name(),
		"args":   salesBIAuditArgs(report.Name(), args),
		"result": salesBIAuditResult(report.Name(), result),
	}, rc.Meta))
	delete(rc.Vars, salesBIReportPendingKey)
	verdict := governedToolResult(report.Name(), result)
	if !verdict.Terminal || verdict.SealedAnswer == "" {
		return errors.New("sales_report_generate publish returned no sealed answer")
	}
	answer := messages.Chat(messages.RoleAssistant, verdict.SealedAnswer, rc.Meta)
	rc.Messages = append(rc.Messages, answer)
	rc.Emit(answer)
	middlewares.SetFinal(rc, verdict.SealedAnswer)
	return nil
}
