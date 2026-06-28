package main

import (
	"github.com/cloudwego/hertz/pkg/app/server"

	"github.com/orka-oss/orka_control_layer/api"
	"github.com/orka-oss/orka_control_layer/cors"
)

// registerRoutes wires CORS, health, public auth and the protected API group.
func registerRoutes(h *server.Hertz, a *api.API, corsHosts []string) {
	h.Use(cors.Middleware(corsHosts))

	h.GET("/health", a.Health)

	g := h.Group("/api/v1/controller")

	// public auth
	g.POST("/auth/register", a.Register)
	g.POST("/auth/login", a.Login)
	// public webhook trigger — the opaque token in the path is the auth.
	g.POST("/hook/:token", a.Webhook)
	// public artifact pages — the share token in the query is the auth.
	g.GET("/pub/a/:slug", a.GetPublicArtifact)
	g.GET("/pub/a/:slug/stream", a.PublicArtifactStream)

	// everything below requires a valid session token
	g.Use(a.AuthMiddleware())

	g.POST("/auth/me", a.Me)

	g.POST("/conversation/create-conversation", a.CreateConversation)
	g.POST("/conversation/get-conversation", a.GetConversation)
	g.POST("/conversation/get-messages", a.GetMessages)
	g.POST("/conversation/search", a.SearchConversations) // cross-conversation full-text search
	g.POST("/conversation/fork", a.ForkConversation)      // branch a conversation at a turn
	g.POST("/conversation/list", a.ListConversations)
	g.POST("/conversation/rename", a.RenameConversation)
	g.POST("/conversation/delete", a.DeleteConversation)
	g.POST("/conversation/prune-empty", a.PruneConversations)
	g.POST("/conversation/share", a.ShareConversation)       // owner grants/revokes access
	g.POST("/conversation/shared-with-me", a.SharedWithMe)   // conversations others shared with me

	// live artifacts — shareable, auto-updating visualization pages
	g.POST("/artifact/list", a.ListArtifacts)
	g.POST("/artifact/by-conversation", a.ArtifactByConversation)
	g.POST("/artifact/get", a.GetArtifact)
	g.POST("/artifact/versions", a.ArtifactVersions)
	g.POST("/artifact/share", a.ShareArtifact)
	g.POST("/artifact/visibility", a.SetArtifactVisibility)
	g.POST("/artifact/delete", a.DeleteArtifact)
	g.GET("/artifact/stream", a.ArtifactStream)

	g.POST("/task/create", a.CreateTask)
	g.POST("/task/get-tasks", a.GetTasks)
	g.POST("/task/schedule", a.ScheduleTask)
	g.POST("/task/unschedule", a.UnscheduleTask)
	g.POST("/task/webhook/enable", a.EnableWebhook)
	g.POST("/task/webhook/disable", a.DisableWebhook)

	g.POST("/chat/run", a.ChatRun)
	g.GET("/chat/attach", a.ChatAttach) // reconnect + replay missed SSE events
	g.POST("/chat/kill", a.ChatKill)
	g.POST("/internal/chat/run", a.ChatRun)

	g.POST("/file/upload", a.FileUpload)
	g.POST("/file/upload-chunk", a.FileUploadChunk)
	g.GET("/file/upload-progress", a.FileUploadProgress)
	g.POST("/file/get-file-url", a.GetFileURL)
	g.GET("/file/download", a.FileDownload)
	g.POST("/file/list", a.FileList)
	g.POST("/file/delete", a.FileDelete) // POST to match the client + the other /*/delete endpoints
	g.POST("/file/versions", a.FileVersions) // overwrite history of a file
	g.POST("/file/restore", a.FileRestore)   // roll a file back to a version

	g.GET("/events", a.Events) // per-user SSE bus: push UI-invalidation signals
	g.GET("/metrics", a.MetricsSnapshot)
	g.GET("/models", a.ListModels)
	g.GET("/tools/catalog", a.ToolsCatalog) // available tools + descriptions for the picker
	g.POST("/chat/confirm", a.ConfirmAction) // approve/reject a paused risky tool call
	g.POST("/chat/followups", a.Followups) // suggested next questions for a Q&A turn

	g.POST("/run/list", a.ListRuns)   // execution history (run records)
	g.POST("/run/get", a.GetRun)
	g.POST("/run/rerun", a.RerunRun)

	g.POST("/connector/list", a.ListConnectors) // external MCP integrations
	g.POST("/connector/create", a.CreateConnector)
	g.POST("/connector/test", a.TestConnector)
	g.POST("/connector/delete", a.DeleteConnector)

	g.POST("/notification/list", a.ListNotifications)
	g.POST("/notification/read", a.ReadNotifications)

	g.POST("/workflow/list", a.ListWorkflows) // definable multi-step pipelines
	g.POST("/workflow/create", a.CreateWorkflow)
	g.POST("/workflow/delete", a.DeleteWorkflow)
	g.POST("/workflow/run", a.RunWorkflow)
	g.POST("/quant/pipeline/run", a.RunFactorPipeline) // research-report → quant-factor pipeline (batch over reports/)
	g.POST("/quant/factors", a.ListFactors)            // the factor library (pipeline output)
	g.POST("/quant/portfolios", a.ListPortfolios)      // weighted portfolios
	g.POST("/quant/factor/status", a.SetFactorStatus)  // human review: approve / reject a factor

	g.POST("/skill/list", a.ListSkills)       // catalog (builtin + installed)
	g.POST("/skill/get", a.GetSkill)          // one skill's full content (for preview)
	g.POST("/skill/install", a.InstallSkill)  // download + register a SKILL.md from a URL
	g.POST("/skill/delete", a.DeleteSkill)    // remove a non-builtin (installed/custom) skill
}
