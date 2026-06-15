// Package service holds the chat runtime: the end-to-end chat path (adk_chat.go)
// running on the cloudwego/eino library (eino_chat.go).
package service

import (
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/obs"
)

// PipelineDeps are the dependencies the chat path threads into the eino runtime.
type PipelineDeps struct {
	LLM          llm.Client
	Model        string
	SystemPrompt string
	Metrics      *obs.Metrics
	SkillDir     string
	Skill        string
}
