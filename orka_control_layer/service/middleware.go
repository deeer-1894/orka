// Package service holds the chat runtime: pipeline assembly (middleware.go) and
// the end-to-end chat path (adk_chat.go, Phase 4).
package service

import (
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/obs"
	"github.com/orka-oss/orka_control_layer/service/middlewares"
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
//	simple    : setup -> memory -> tools -> interrupt -> output
//	interrupt : setup -> memory -> tools -> interrupt -> output  (clarify is data-driven)
//	complex   : setup -> plan -> skill -> memory -> tools -> interrupt -> output
//
// memory-mid (history trimming) runs before tools-mid so the seeded context is
// bounded; tools-mid additionally re-trims each ReAct iteration.
//
// interrupt-mid is included everywhere as a harmless finalizer; in practice
// tools-mid raises clarify itself so the cursor lands correctly for resume.
func BuildPipeline(scene Scene, deps PipelineDeps) []agent.Middleware {
	// maxHistory bounds the LLM context: Memory trims the seeded history before
	// the loop, and tools-mid re-trims each ReAct iteration so an agentic run
	// with many tool calls cannot grow context (and cost) without bound.
	const maxHistory = 40
	// maxIters caps the ReAct loop. Tool calls within one iteration run
	// concurrently, so 16 batches comfortably covers complex multi-tool tasks
	// (research + compute + synthesize + write) without runaway loops.
	const maxIters = 16
	setup := &middlewares.Setup{SystemPrompt: deps.SystemPrompt}
	memory := &middlewares.Memory{MaxMessages: maxHistory}
	tools := &middlewares.Tools{LLM: deps.LLM, Model: deps.Model, Metrics: deps.Metrics, MaxHistory: maxHistory, MaxIters: maxIters}
	interrupt := &middlewares.Interrupt{}
	output := &middlewares.Output{}

	switch scene {
	case SceneComplex:
		return []agent.Middleware{
			setup,
			&middlewares.Plan{LLM: deps.LLM, Model: deps.Model},
			&middlewares.Skill{Dir: deps.SkillDir, Skill: deps.Skill},
			memory,
			tools,
			interrupt,
			output,
		}
	default: // simple, interrupt
		return []agent.Middleware{setup, memory, tools, interrupt, output}
	}
}
