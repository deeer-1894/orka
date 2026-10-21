package db

import (
	"testing"
	"time"
)

func TestTriggerNormalizeRejectsInvalidAndClearsOtherKinds(t *testing.T) {
	for name, tr := range map[string]TaskTrigger{
		"unknown kind":     {Kind: "cron"},
		"bad cron":         {Kind: TriggerSchedule, Cron: "61 * * * *"},
		"cron and at":      {Kind: TriggerSchedule, Cron: "0 9 * * *", At: 1},
		"bad timezone":     {Kind: TriggerSchedule, Cron: "0 9 * * *", Timezone: "Mars/Base"},
		"non-http watch":   {Kind: TriggerCondition, WatchURL: "file:///etc/passwd"},
		"too-fast polling": {Kind: TriggerCondition, WatchURL: "https://example.com", CheckEverySec: 30},
	} {
		if _, err := tr.Normalize(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	got, err := TaskTrigger{Kind: TriggerCondition, WatchURL: "https://example.com/p", Cron: "0 9 * * *", Event: "x"}.Normalize()
	if err != nil || got.Cron != "" || got.Event != "" || got.CheckEverySec != defaultConditionCheckSec {
		t.Fatalf("condition normalize = %+v %v", got, err)
	}
}

func TestNextDueFollowsEachTriggerKind(t *testing.T) {
	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	friday := time.Date(2026, 10, 23, 21, 0, 0, 0, shanghai).UnixMilli()
	weekdays := TaskMeta{Trigger: &TaskTrigger{Kind: TriggerSchedule, Cron: "18 20 * * 1-5", Timezone: "Asia/Shanghai"}}
	if got, want := NextDue(weekdays, friday), time.Date(2026, 10, 26, 20, 18, 0, 0, shanghai).UnixMilli(); got != want {
		t.Errorf("weekday cron next = %v want %v", time.UnixMilli(got).In(shanghai), time.UnixMilli(want).In(shanghai))
	}
	once := TaskMeta{Trigger: &TaskTrigger{Kind: TriggerSchedule, At: friday + 1000}}
	if NextDue(once, friday) != friday+1000 || NextDue(once, friday+1000) != 0 {
		t.Error("one-off schedule must fire once, then have nothing due")
	}
	watch := TaskMeta{Trigger: &TaskTrigger{Kind: TriggerCondition, CheckEverySec: 600}}
	if NextDue(watch, friday) != friday+600_000 {
		t.Error("condition check interval")
	}
	if NextDue(TaskMeta{Trigger: &TaskTrigger{Kind: TriggerEvent}}, friday) != 0 {
		t.Error("event tasks are never time-scheduled")
	}
	if NextDue(TaskMeta{IntervalSec: 60}, friday) != friday+60_000 || NextDue(TaskMeta{}, friday) != 0 {
		t.Error("legacy interval cadence")
	}
}
