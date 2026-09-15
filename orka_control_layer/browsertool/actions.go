package browsertool

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func waitFor(ctx context.Context, s Session, r Request) error {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		reply, err := page(ctx, s, "wait", r)
		if err != nil {
			return err
		}
		if reply.Ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func act(ctx context.Context, s Session, r Request) error {
	reply, err := page(ctx, s, r.Action, r)
	if err != nil {
		return err
	}
	switch r.Action {
	case "click":
		for _, kind := range []string{"mousePressed", "mouseReleased"} {
			if err = s.Lease.Execute(ctx, "Input.dispatchMouseEvent", map[string]any{"type": kind, "x": reply.X, "y": reply.Y, "button": "left", "clickCount": 1}, nil); err != nil {
				return err
			}
		}
	case "press":
		key, _ := parseKey(r.Key) // validated before acquiring the lease
		for _, kind := range []string{"keyDown", "keyUp"} {
			params := map[string]any{"type": kind, "key": key.key, "code": key.code, "windowsVirtualKeyCode": key.virtual, "nativeVirtualKeyCode": key.virtual, "modifiers": key.modifiers}
			if kind == "keyDown" && key.text != "" {
				params["text"] = key.text
				params["unmodifiedText"] = key.text
			}
			if err = s.Lease.Execute(ctx, "Input.dispatchKeyEvent", params, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

type keySpec struct {
	key, code, text    string
	virtual, modifiers int
}

// Modifier flags are CDP's Alt=1, Control=2, Meta=4, Shift=8. Text is supplied
// only for ordinary characters/Enter, never for shortcut combinations.
func parseKey(value string) (keySpec, error) {
	bad := func() (keySpec, error) {
		return keySpec{}, NewActionError("invalid_request", "Unsupported key or modifier combination.")
	}
	if len(value) > 64 || value == "" {
		return bad()
	}
	parts := strings.Split(value, "+")
	if value == "+" {
		parts = []string{"+"}
	} else if strings.HasSuffix(value, "++") {
		parts = append(strings.Split(strings.TrimSuffix(value, "++"), "+"), "+")
	}
	var result keySpec
	for _, modifier := range parts[:len(parts)-1] {
		bit := 0
		switch modifier {
		case "Alt":
			bit = 1
		case "Ctrl", "Control":
			bit = 2
		case "Meta":
			bit = 4
		case "Shift":
			bit = 8
		default:
			return bad()
		}
		if result.modifiers&bit != 0 {
			return bad()
		}
		result.modifiers |= bit
	}
	last := parts[len(parts)-1]
	named := map[string]int{"Enter": 13, "Tab": 9, "Escape": 27, "Backspace": 8, "Delete": 46, "ArrowLeft": 37, "ArrowUp": 38, "ArrowRight": 39, "ArrowDown": 40, "Home": 36, "End": 35, "PageUp": 33, "PageDown": 34, "Space": 32}
	if code, ok := named[last]; ok {
		result.key = last
		result.code = last
		result.virtual = code
		if last == "Space" {
			result.key = " "
			result.text = " "
		}
		if last == "Enter" {
			result.text = "\r"
		}
	} else {
		r, n := utf8.DecodeRuneInString(last)
		if n != len(last) || r == utf8.RuneError || unicode.IsControl(r) {
			return bad()
		}
		result.key = last
		result.text = last
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			result.code = "Key" + strings.ToUpper(last)
			result.virtual = int(unicode.ToUpper(r))
		}
		if r >= '0' && r <= '9' {
			result.code = "Digit" + last
			result.virtual = int(r)
		}
	}
	if result.modifiers&7 != 0 {
		result.text = ""
	}
	return result, nil
}
