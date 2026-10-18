package llm

import (
	"encoding/json"
	"github.com/cloudwego/eino/components/model"
	"testing"
)

func TestReviewExplicitZeroTemperature(t *testing.T) {
	req := NewEinoModel(nil, "m").request(nil, []model.Option{model.WithTemperature(0)})
	b, err := json.Marshal(toWireRequest(req))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(b, &got)
	if _, ok := got["temperature"]; !ok {
		t.Fatalf("explicit temperature=0 lost on wire: %s", b)
	}
}
