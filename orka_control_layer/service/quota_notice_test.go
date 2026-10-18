package service

import (
	"github.com/orka-oss/orka_control_layer/llm"
	"strings"
	"testing"
)

func TestQuotaNoticeShowsResetWithoutProviderDetails(t *testing.T) {
	err := &llm.APIError{Status: 429, Body: `{"error":{"code":"AccountQuotaExceeded","message":"It will reset at 2026-09-14 20:30:54 +0800 CST. Request id: private-request"}}`}
	notice := friendlyErr(err)
	if !strings.Contains(notice, "额度已用尽") || !strings.Contains(notice, "2026-09-14 20:30:54 +0800") || strings.Contains(notice, "private-request") {
		t.Fatal(notice)
	}
	if strings.Contains(friendlyErr(&llm.APIError{Status: 429}), "额度已用尽") {
		t.Fatal("burst limit mislabeled as exhausted quota")
	}
}
