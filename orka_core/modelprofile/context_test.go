package modelprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSnapshotIsFrozenAndPrivate(t *testing.T) {
	s := Snapshot{ProfileID: "work", Protocol: OpenAICompatible, BaseURL: "https://provider.test/v1", Model: "chosen", APIKey: "private-key", Capabilities: Capabilities{Vision: true}}
	ctx := WithContext(context.Background(), s)
	s.Model = "later"
	got, ok := FromContext(ctx)
	if !ok || got.Model != "chosen" || !got.Capabilities.Vision || got.APIKey != "private-key" {
		t.Fatal("snapshot not retained")
	}
	got.Model = "mutated"
	again, _ := FromContext(ctx)
	if again.Model != "chosen" {
		t.Fatal("context changed")
	}
	b, _ := json.Marshal(got)
	for _, out := range []string{string(b), fmt.Sprintf("%v %+v %#v", got, got, got)} {
		if strings.Contains(out, "private-key") {
			t.Fatal("secret serialized or logged")
		}
	}
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("invented snapshot")
	}
}
