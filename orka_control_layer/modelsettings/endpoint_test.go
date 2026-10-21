package modelsettings

import (
	"reflect"
	"testing"
)

func TestEndpointForOnlyDescribesKnownHosts(t *testing.T) {
	ark := Endpoint{MaxOutputTokens: 16384}
	for base, want := range map[string]Endpoint{
		"https://ark.cn-beijing.volces.com/api/plan/v3":   ark,
		"https://ark.cn-beijing.volces.com/api/coding/v3": ark,
		"https://ark.ap-southeast.volces.com/api/v3":      ark,
		"https://api.openai.com/v1":                       {},
		"http://localhost:11434/v1":                       {},
		"https://volces.com.example.test/v1":              {},
		"::not a url":                                     {},
	} {
		if got := EndpointFor(base); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %+v want %+v", base, got, want)
		}
	}
}
