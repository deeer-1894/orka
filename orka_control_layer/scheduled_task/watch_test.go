package scheduled_task

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/orka-oss/orka_control_layer/db"
)

func TestPageTextIgnoresMarkupScriptsAndStyles(t *testing.T) {
	got := pageText(`<html><style>.a{}</style><script>var t=Date.now()</script><p class="x1">价格 &amp; 库存</p>  <b>有货</b></html>`)
	if got != "价格 & 库存 有货" {
		t.Fatalf("pageText = %q", got)
	}
}

func TestObserveFiresOnChangeAfterBaselineAndOnNewlyPresentText(t *testing.T) {
	page := "<p>v1</p>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(page)) }))
	defer srv.Close()
	ctx, client := context.Background(), srv.Client()
	change := db.TaskTrigger{Kind: db.TriggerCondition, WatchURL: srv.URL}
	first, err := Observe(ctx, client, change)
	if err != nil || first.Fire {
		t.Fatalf("baseline must not fire: %+v %v", first, err)
	}
	change.Fingerprint = first.Fingerprint
	if same, _ := Observe(ctx, client, change); same.Fire {
		t.Fatal("unchanged page fired")
	}
	page = "<p>v2</p>"
	if changed, _ := Observe(ctx, client, change); !changed.Fire || !strings.Contains(changed.Reason, "变化") {
		t.Fatalf("changed page did not fire: %+v", changed)
	}
	appear := db.TaskTrigger{Kind: db.TriggerCondition, WatchURL: srv.URL, Contains: "有货"}
	if o, _ := Observe(ctx, client, appear); o.Fire || o.Fingerprint != "absent" {
		t.Fatalf("absent text fired: %+v", o)
	}
	page = "<p>现在有货</p>"
	appear.Fingerprint = "absent"
	o, _ := Observe(ctx, client, appear)
	if !o.Fire || o.Fingerprint != "present" {
		t.Fatalf("appearing text did not fire: %+v", o)
	}
	appear.Fingerprint = o.Fingerprint
	if again, _ := Observe(ctx, client, appear); again.Fire {
		t.Fatal("text still present must not fire again")
	}
}

func TestWatchClientRefusesNonPublicAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("internal")) }))
	defer srv.Close()
	if _, err := Observe(context.Background(), WatchClient(), db.TaskTrigger{WatchURL: srv.URL}); err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("loopback fetch allowed: %v", err)
	}
	for _, ip := range []string{"10.0.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "::1", "fc00::1"} {
		if publicIP(net.ParseIP(ip)) {
			t.Errorf("%s treated as public", ip)
		}
	}
	if !publicIP(net.ParseIP("93.184.216.34")) {
		t.Error("public address refused")
	}
}
