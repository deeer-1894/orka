package llm

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

const recordedQuotaBody = `{"error":{"code":"AccountQuotaExceeded","message":"You have exceeded the 5-hour usage quota. It will reset at 2026-09-14 20:30:54 +0800 CST. Request id: private-request","type":"TooManyRequests"}}`

func TestAccountQuotaDoesNotRepeatRejectedCalls(t *testing.T) {
	original := &APIError{Status: 429, Body: recordedQuotaBody}
	client := &scriptedClient{errs: []error{fmt.Errorf("provider: %w", original), nil}}
	_, err := fastRetry(client, 3).Chat(context.Background(), Request{})
	if !errors.Is(err, original) || client.calls != 1 {
		t.Fatalf("quota was retried: calls=%d err=%v", client.calls, err)
	}
}

func TestQuotaClassificationDoesNotConfuseBurstLimits(t *testing.T) {
	for _, body := range []string{`{"error":{"code":"RequestBurstTooFast"}}`, `{"error":{"type":"rate_limit_exceeded","message":"quota temporarily exceeded"}}`, `not JSON`} {
		e := &APIError{Status: 429, Body: body}
		if _, ok := QuotaExhaustion(e); ok || !IsTransient(context.Background(), e) {
			t.Fatal("burst/unknown limit lost retry", body)
		}
	}
	for _, body := range []string{recordedQuotaBody, `{"error":{"code":"insufficient_quota"}}`, `{"error":{"type":"billing_hard_limit_reached"}}`} {
		if _, ok := QuotaExhaustion(&APIError{Status: 429, Body: body}); !ok {
			t.Fatal(body)
		}
	}
	reset, ok := QuotaExhaustion(fmt.Errorf("wrapped: %w", &APIError{Status: 429, Body: recordedQuotaBody}))
	if !ok || reset.Format("2006-01-02 15:04:05 -0700") != "2026-09-14 20:30:54 +0800" {
		t.Fatalf("reset=%v", reset)
	}
}
