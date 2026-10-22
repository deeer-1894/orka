package main

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTurnsFollowTheFollowupInOrder(t *testing.T) {
	var s suite
	src := `
tasks:
  - id: chat
    max_repeat: 1
    prompt: first
    followup: {prompt: second}
    turns:
      - prompt: third
        expect: {contains: [x]}
      - prompt: fourth
`
	if err := yaml.Unmarshal([]byte(src), &s); err != nil {
		t.Fatal(err)
	}
	later := s.Tasks[0].laterTurns()
	if len(later) != 3 || later[0].Prompt != "second" || later[1].Prompt != "third" || later[2].Prompt != "fourth" {
		t.Fatalf("later turns = %+v", later)
	}
	if got := laterExpects(s.Tasks[0]); len(got) != 3 || got[1].Contains[0] != "x" {
		t.Fatalf("expectations = %+v", got)
	}
	if s.Tasks[0].MaxRepeat != 1 {
		t.Fatal("max_repeat not parsed")
	}
}
