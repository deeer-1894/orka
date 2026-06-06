package main

import (
	"github.com/cloudwego/hertz/pkg/app/server"

	"github.com/cavis-oss/cavis_control_layer/api"
	"github.com/cavis-oss/cavis_control_layer/cors"
)

// registerRoutes wires CORS, health and the /api/v1/controller route group.
func registerRoutes(h *server.Hertz, a *api.API, corsHosts []string) {
	h.Use(cors.Middleware(corsHosts))

	h.GET("/health", a.Health)

	g := h.Group("/api/v1/controller")
	{
		g.POST("/conversation/create-conversation", a.CreateConversation)
		g.POST("/conversation/get-conversation", a.GetConversation)
		g.POST("/conversation/get-messages", a.GetMessages)

		g.POST("/task/create", a.CreateTask)
		g.POST("/task/get-tasks", a.GetTasks)

		g.POST("/chat/run", a.ChatRun)
		g.POST("/chat/kill", a.ChatKill)
		g.POST("/internal/chat/run", a.ChatRun)

		g.POST("/file/upload", a.FileUpload)
		g.POST("/file/upload-chunk", a.FileUploadChunk)
		g.GET("/file/upload-progress", a.FileUploadProgress)
		g.POST("/file/get-file-url", a.GetFileURL)
		g.GET("/file/download", a.FileDownload)
		g.POST("/file/list", a.FileList)
		g.DELETE("/file/delete", a.FileDelete)

		g.GET("/metrics", a.MetricsSnapshot)
	}
}
