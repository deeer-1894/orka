package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/redis/go-redis/v9"

	"github.com/cavis-oss/cavis_core/config"
	"github.com/cavis-oss/cavis_core/messages"
	"github.com/cavis-oss/cavis_core/state"
	"github.com/cavis-oss/cavis_core/trace"
	"github.com/cavis-oss/cavis_control_layer/api"
	"github.com/cavis-oss/cavis_control_layer/checkpoint"
	"github.com/cavis-oss/cavis_control_layer/connectors"
	"github.com/cavis-oss/cavis_control_layer/scheduled_task"
	"github.com/cavis-oss/cavis_control_layer/db"
	"github.com/cavis-oss/cavis_control_layer/llm"
	"github.com/cavis-oss/cavis_control_layer/message_utils"
	"github.com/cavis-oss/cavis_control_layer/obs"
	"github.com/cavis-oss/cavis_control_layer/service"
)

// waitReady polls an HTTP endpoint until it accepts connections (or timeout),
// so the control layer doesn't race the tools_server on first request.
func waitReady(url string, timeout time.Duration, logger *slog.Logger) {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	logger.Warn("tools_server not ready within timeout; continuing", "url", url)
}

func main() {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "config.yaml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		panic(err)
	}

	logger := obs.NewLogger(cfg.Obs.LogLevel)
	metrics := obs.NewMetrics()

	// OpenTelemetry tracing (OTLP / stdout / no-op per env).
	shutdownTrace, err := trace.Init(context.Background(), "cavis-control")
	if err != nil {
		logger.Error("trace init", "err", err)
	} else {
		defer func() { _ = shutdownTrace(context.Background()) }()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store, err := db.NewStorage(ctx, cfg.Storage.MongoURI, cfg.Storage.MongoDB)
	if err != nil {
		logger.Error("mongo init failed", "err", err)
		os.Exit(1)
	}

	rdb := redis.NewClient(&redis.Options{Addr: cfg.Storage.RedisAddr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.Error("redis init failed", "err", err)
		os.Exit(1)
	}
	cpStore := checkpoint.NewRedisStore(rdb, "cavis:cp:")

	// LLM: real OpenAI-compatible client, or a deterministic demo client for
	// offline smoke tests (LLM_MOCK=1).
	var mainLLM, miniLLM llm.Client
	if os.Getenv("LLM_MOCK") == "1" {
		mainLLM, miniLLM = llm.NewDemo(), llm.NewDemo()
		logger.Info("using demo LLM (LLM_MOCK=1)")
	} else {
		mainLLM = llm.NewOpenAIClient(cfg.LLM.OpenAIBaseURL, cfg.LLM.OpenAIAPIKey)
		miniLLM = mainLLM
	}

	msg := message_utils.New(store, cfg.Obs.PersistSampling, logger)
	chat := service.NewChatService(cfg, mainLLM, miniLLM, cpStore, msg, metrics, logger)

	// Wire the real GUI executor (run_agent) when configured; else keep the mock.
	if wsURL := cfg.Agent.GUIAgentWSURL; wsURL != "" {
		service.GUITool = connectors.NewRunAgentTool(wsURL)
		logger.Info("gui agent enabled", "ws", wsURL)
	}

	// Optionally source tools from a remote tools_server over MCP. When unset,
	// the control layer uses the local filesystem tools directly.
	if toolsURL := os.Getenv("TOOLS_MCP_URL"); toolsURL != "" {
		ttl := time.Duration(cfg.Security.CtxTokenTTLSec) * time.Second
		chat.ToolsFor = service.MCPToolsProvider(
			cfg.Storage.BaseStoragePath, toolsURL, cfg.Security.CtxTokenSecret,
			ttl, []string{"file:read", "file:write", "web:search"},
		)
		waitReady(toolsURL, 15*time.Second, logger) // avoid first-request startup race
		logger.Info("tools via MCP", "url", toolsURL)
	}

	logger.Info("control layer starting",
		"addr", cfg.Server.ControlAddr,
		"mongo", cfg.Storage.MongoDB,
		"redis", cfg.Storage.RedisAddr,
		"run_mode", cfg.Agent.RunMode,
		"cors_hosts", cfg.Server.CORSAllowedHosts,
	)

	a := api.New(store, logger, metrics, chat)
	a.BaseStorage = cfg.Storage.BaseStoragePath
	a.Secret = cfg.Security.CtxTokenSecret
	a.Directory = connectors.NewCachedDirectory(
		&connectors.StubDirectory{},
		state.NewRedisStore(rdb, "cavis:dir:"),
		time.Hour,
	)

	// Optional cron scheduler: render task templates and trigger chat runs.
	if os.Getenv("SCHEDULER_ENABLE") == "1" {
		sched := &scheduled_task.Scheduler{
			Source: scheduled_task.CronSource(store),
			Trigger: func(ctx context.Context, task db.TaskMeta, content string) error {
				chat.Run(context.Background(), service.ChatRunRequest{
					Message: content, ConversationID: task.ConversationID,
					TaskID: task.TaskID, UserEmail: task.OwnerEmail,
				}, func(messages.Message) {})
				return nil
			},
			Interval: time.Minute,
			Log:      logger,
		}
		go sched.Start(context.Background())
		logger.Info("scheduler enabled")
	}

	h := server.New(server.WithHostPorts(cfg.Server.ControlAddr))
	registerRoutes(h, a, cfg.Server.CORSAllowedHosts)
	h.Spin()
}
