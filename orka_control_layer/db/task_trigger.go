package db

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/robfig/cron/v3"
)

// Trigger kinds. A task without a Trigger is a legacy fixed-interval task.
const (
	TriggerSchedule  = "schedule"  // calendar time: cron or a single moment
	TriggerCondition = "condition" // a watched page changes or starts containing text
	TriggerEvent     = "event"     // an external system calls the task's webhook
)

const (
	minConditionCheckSec     = 300
	defaultConditionCheckSec = 3600
)

// TaskTrigger says when a task runs.
type TaskTrigger struct {
	Kind string `bson:"kind" json:"kind"`
	// Schedule: a standard 5-field cron expression evaluated in Timezone, or At
	// (unix millis) for a single run.
	Cron     string `bson:"cron,omitempty" json:"cron,omitempty"`
	At       int64  `bson:"at,omitempty" json:"at,omitempty"`
	Timezone string `bson:"timezone,omitempty" json:"timezone,omitempty"`
	// Condition: WatchURL is checked every CheckEverySec. With Contains the task
	// fires when the text appears; without it, when the page text changes.
	WatchURL      string `bson:"watch_url,omitempty" json:"watch_url,omitempty"`
	Contains      string `bson:"contains,omitempty" json:"contains,omitempty"`
	CheckEverySec int64  `bson:"check_every_sec,omitempty" json:"check_every_sec,omitempty"`
	Fingerprint   string `bson:"fingerprint,omitempty" json:"-"`
	// Event: an optional filter on the webhook's event name, e.g.
	// "pull_request.closed" for GitHub; empty accepts every call.
	Event string `bson:"event,omitempty" json:"event,omitempty"`
}

// Normalize validates a trigger and fills defaults.
func (t TaskTrigger) Normalize() (TaskTrigger, error) {
	t.Kind = strings.TrimSpace(t.Kind)
	switch t.Kind {
	case TriggerSchedule:
		if (t.Cron == "") == (t.At == 0) {
			return t, errors.New("时间触发需要设置周期或单次时间之一")
		}
		if _, err := t.location(); err != nil {
			return t, err
		}
		if t.Cron != "" {
			if _, err := cron.ParseStandard(t.Cron); err != nil {
				return t, fmt.Errorf("Cron 表达式无效：%s", t.Cron)
			}
		}
		t.WatchURL, t.Contains, t.CheckEverySec, t.Fingerprint, t.Event = "", "", 0, "", ""
	case TriggerCondition:
		u, err := url.Parse(strings.TrimSpace(t.WatchURL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(t.WatchURL) > 2048 {
			return t, errors.New("监控网址必须是 http(s) 链接")
		}
		t.WatchURL = u.String()
		t.Contains = strings.TrimSpace(t.Contains)
		if utf8.RuneCountInString(t.Contains) > 200 {
			return t, errors.New("关键文字不超过 200 字")
		}
		if t.CheckEverySec == 0 {
			t.CheckEverySec = defaultConditionCheckSec
		}
		if t.CheckEverySec < minConditionCheckSec || t.CheckEverySec > 7*24*3600 {
			return t, fmt.Errorf("检查间隔需在 %d 分钟到 7 天之间", minConditionCheckSec/60)
		}
		t.Cron, t.At, t.Timezone, t.Event = "", 0, "", ""
	case TriggerEvent:
		t.Event = strings.TrimSpace(t.Event)
		if len(t.Event) > 100 {
			return t, errors.New("事件过滤不超过 100 个字符")
		}
		t.Cron, t.At, t.Timezone, t.WatchURL, t.Contains, t.CheckEverySec, t.Fingerprint = "", 0, "", "", "", 0, ""
	default:
		return t, fmt.Errorf("未知的触发方式：%s", t.Kind)
	}
	return t, nil
}

func (t TaskTrigger) location() (*time.Location, error) {
	if t.Timezone == "" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(t.Timezone)
	if err != nil {
		return nil, fmt.Errorf("未知的时区：%s", t.Timezone)
	}
	return loc, nil
}

// NextDue is when the scheduler should next consider task after now (unix
// millis), or 0 when it has no further scheduled time: event tasks, a single
// run already past, or an invalid stored trigger.
func NextDue(task TaskMeta, now int64) int64 {
	t := task.Trigger
	if t == nil {
		if task.IntervalSec <= 0 || now < 0 || task.IntervalSec > (math.MaxInt64-now)/1000 {
			return 0
		}
		return now + task.IntervalSec*1000
	}
	switch t.Kind {
	case TriggerSchedule:
		if t.At > 0 {
			if t.At > now {
				return t.At
			}
			return 0
		}
		spec, err := cron.ParseStandard(t.Cron)
		loc, locErr := t.location()
		if err != nil || locErr != nil {
			return 0
		}
		return spec.Next(time.UnixMilli(now).In(loc)).UnixMilli()
	case TriggerCondition:
		return now + t.CheckEverySec*1000
	}
	return 0
}
