// Package service holds the chat runtime: pipeline assembly (middleware.go) and
// the end-to-end chat path (adk_chat.go, Phase 4).
package service

import (
	"github.com/cavis-oss/cavis_core/agent"
	"github.com/cavis-oss/cavis_control_layer/llm"
	"github.com/cavis-oss/cavis_control_layer/obs"
	"github.com/cavis-oss/cavis_control_layer/service/middlewares"
)

// Scene selects which middleware chain to assemble.
type Scene string

const (
	SceneSimple    Scene = "simple"
	SceneComplex   Scene = "complex"
	SceneInterrupt Scene = "interrupt"
)

// PipelineDeps are the dependencies injected into the middleware chain.
type PipelineDeps struct {
	LLM          llm.Client
	Model        string
	SystemPrompt string
	Metrics      *obs.Metrics
	SkillDir     string
	Skill        string
}

// BuildPipeline assembles the middleware chain for a scene.
//
//	simple    : setup -> tools -> interrupt -> output
//	interrupt : setup -> tools -> interrupt -> output   (same; clarify is data-driven)
//	complex   : setup -> plan -> skill -> tools -> memory -> interrupt -> output
//
// interrupt-mid is included everywhere as a harmless finalizer; in practice
// tools-mid raises clarify itself so the cursor lands correctly for resume.
func BuildPipeline(scene Scene, deps PipelineDeps) []agent.Middleware {
	setup := &middlewares.Setup{SystemPrompt: deps.SystemPrompt}
	tools := &middlewares.Tools{LLM: deps.LLM, Model: deps.Model, Metrics: deps.Metrics}
	interrupt := &middlewares.Interrupt{}
	output := &middlewares.Output{}

	switch scene {
	case SceneComplex:
		return []agent.Middleware{
			setup,
			&middlewares.Plan{LLM: deps.LLM, Model: deps.Model},
			&middlewares.Skill{Dir: deps.SkillDir, Skill: deps.Skill},
			tools,
			&middlewares.Memory{},
			interrupt,
			output,
		}
	default: // simple, interrupt
		return []agent.Middleware{setup, tools, interrupt, output}
	}
}
