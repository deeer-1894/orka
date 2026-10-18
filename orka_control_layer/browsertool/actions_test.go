package browsertool

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestPressSendsKeyAndTextWithoutShortcutInsertion(t *testing.T) {
	for _, test := range []struct {
		key, text string
		modifiers int
	}{{"Enter", "\r", 0}, {"Ctrl+a", "", 2}, {"Shift+ArrowLeft", "", 8}, {"+", "+", 0}, {"Control++", "", 2}, {"文", "文", 0}} {
		t.Run(test.key, func(t *testing.T) {
			lease := &fixtureLease{}
			events := []map[string]any{}
			lease.handler = func(method string, params, out any) (bool, error) {
				if method != "Input.dispatchKeyEvent" {
					return false, nil
				}
				data, _ := json.Marshal(params)
				var event map[string]any
				_ = json.Unmarshal(data, &event)
				events = append(events, event)
				return true, nil
			}
			result, err := NewEngine(&fixtureDialer{lease: lease}).Run(context.Background(), testIdentity(), Request{Action: "press", Selector: "input", Key: test.key}, nil)
			if err != nil || !result.OK || len(events) != 2 {
				t.Fatalf("%+v %v events=%v", result, err, events)
			}
			text, _ := events[0]["text"].(string)
			if text != test.text || events[0]["modifiers"] != float64(test.modifiers) || events[0]["type"] != "keyDown" || events[1]["type"] != "keyUp" {
				t.Fatal(events)
			}
			if _, ok := events[1]["text"]; ok {
				t.Fatal("keyUp inserted duplicate text")
			}
		})
	}
}

func TestWaitDeadlineClosesLease(t *testing.T) {
	lease := &fixtureLease{}
	lease.handler = func(method string, params, out any) (bool, error) {
		if method != "Orka.observe" {
			return false, nil
		}
		return true, json.Unmarshal([]byte(`{"result":{"value":{"ok":true,"ready":false}}}`), out)
	}
	before := time.Now()
	result, err := NewEngine(&fixtureDialer{lease: lease}).Run(context.Background(), testIdentity(), Request{Action: "wait", Condition: "visible", Selector: "button", TimeoutMS: 10}, nil)
	if err == nil || result.Error.Code != "timeout" || lease.closed != 1 || time.Since(before) > time.Second {
		t.Fatalf("%+v %v", result, err)
	}
}
