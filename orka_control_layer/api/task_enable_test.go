package api

import (
	"testing"

	"github.com/orka-oss/orka_control_layer/db"
)

func TestEnableTaskSetsDueTimePerTriggerKind(t *testing.T) {
	const now = int64(1_000_000)
	watch := &db.TaskMeta{Trigger: &db.TaskTrigger{Kind: db.TriggerCondition, CheckEverySec: 600}, DisabledReason: "breaker", ConsecutiveFails: 5}
	if err := enableTask(watch, true, now); err != nil || watch.NextRunAt != now || watch.CronStatus != "on" || watch.DisabledReason != "" || watch.ConsecutiveFails != 0 {
		t.Fatalf("condition task = %+v %v", watch, err)
	}
	event := &db.TaskMeta{Trigger: &db.TaskTrigger{Kind: db.TriggerEvent}}
	if err := enableTask(event, true, now); err != nil || event.NextRunAt != 0 || event.CronStatus != "on" {
		t.Fatalf("event task = %+v %v", event, err)
	}
	past := &db.TaskMeta{Trigger: &db.TaskTrigger{Kind: db.TriggerSchedule, At: now - 1}}
	if err := enableTask(past, true, now); err != errPastSchedule {
		t.Fatalf("past one-off accepted: %v", err)
	}
	if err := enableTask(past, false, now); err != nil || past.CronStatus != "off" || past.NextRunAt != 0 {
		t.Fatalf("disable = %+v %v", past, err)
	}
}
