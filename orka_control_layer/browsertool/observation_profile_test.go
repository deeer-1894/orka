package browsertool

import (
	"context"
	"encoding/json"
	"testing"
)

func TestObservationProfilesHaveDistinctBoundedBudgets(t *testing.T) {
	tests := []struct {
		name    string
		profile ObservationProfile
		want    observationLimits
	}{
		{name: "default", profile: ObservationDefault, want: observationLimits{textChars: 4000, elementRefs: 40, outputBytes: 10 << 10}},
		{name: "research", profile: ObservationResearch, want: observationLimits{textChars: 6000, elementRefs: 48, outputBytes: 12 << 10}},
		{name: "detailed", profile: ObservationDetailed, want: observationLimits{textChars: MaxSnapshotText, elementRefs: MaxSnapshotRefs, outputBytes: 60000}},
		{name: "unknown falls back to default", profile: ObservationProfile(255), want: observationLimits{textChars: 4000, elementRefs: 40, outputBytes: 10 << 10}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := observationLimitsFrom(WithObservationProfile(context.Background(), test.profile))
			if got != test.want {
				t.Fatalf("limits = %+v, want %+v", got, test.want)
			}
		})
	}

	if got := (observationLimits{}).normalized(); got != observationProfiles[ObservationDefault] {
		t.Fatalf("zero request limits = %+v, want compact defaults %+v", got, observationProfiles[ObservationDefault])
	}
	if got := (observationLimits{textChars: MaxSnapshotText + 1, elementRefs: MaxSnapshotRefs + 1, outputBytes: 60001}).normalized(); got != observationProfiles[ObservationDetailed] {
		t.Fatalf("oversized request limits = %+v, want hard caps %+v", got, observationProfiles[ObservationDetailed])
	}
}

func TestDecodeReplyPreservesDirectLinkTarget(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"ok": true,
		"snapshot": map[string]any{
			"id":   "document-1",
			"text": "",
			"elements": []map[string]any{{
				"ref": "e1", "tag": "a", "role": "link", "name": "Story", "href": "https://news.example/story",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := decodeReply(runtimeReply{Result: struct {
		Value json.RawMessage `json:"value"`
	}{Value: payload}})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Snapshot == nil || len(reply.Snapshot.Elements) != 1 || reply.Snapshot.Elements[0].Href != "https://news.example/story" {
		t.Fatalf("direct link target lost: %+v", reply.Snapshot)
	}
}
