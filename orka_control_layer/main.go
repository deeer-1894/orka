package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/redis/go-redis/v9"

	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/state"
	"github.com/orka-oss/orka_core/trace"
	"github.com/orka-oss/orka_control_layer/api"
	"github.com/orka-oss/orka_control_layer/checkpoint"
	"github.com/orka-oss/orka_control_layer/connectors"
	"github.com/orka-oss/orka_control_layer/scheduled_task"
	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/message_utils"
	"github.com/orka-oss/orka_control_layer/obs"
	"github.com/orka-oss/orka_control_layer/service"
	"github.com/orka-oss/orka_control_layer/service/middlewares"
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
	if err := cfg.Validate(); err != nil {
		panic("config validation failed: " + err.Error())
	}

	logger := obs.NewLogger(cfg.Obs.LogLevel)
	metrics := obs.NewMetrics()

	// OpenTelemetry tracing (OTLP / stdout / no-op per env).
	shutdownTrace, err := trace.Init(context.Background(), "orka-control")
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
	cpStore := checkpoint.NewRedisStore(rdb, "orka:cp:")

	// LLM: real OpenAI-compatible client only (no fake/demo fallback — the
	// server always talks to the configured model).
	if cfg.LLM.OpenAIAPIKey == "" {
		logger.Warn("LLM api key is empty; chat requests will fail until OPENAI_API_KEY is set")
	}
	var mainLLM, miniLLM llm.Client
	// Wrap the provider client in bounded exponential-backoff retry so a transient
	// 429/5xx/network blip doesn't fail a whole agentic run (or a sub-agent).
	mainLLM = llm.NewRetry(
		llm.NewOpenAIClient(cfg.LLM.OpenAIBaseURL, cfg.LLM.OpenAIAPIKey),
		llm.RetryConfig{
			MaxAttempts: cfg.LLM.MaxRetries,
			OnRetry: func(attempt int, delay time.Duration, err error) {
				logger.Warn("llm transient error; retrying", "attempt", attempt, "delay", delay.String(), "err", err.Error())
			},
		},
	)
	miniLLM = mainLLM

	msg := message_utils.New(store, cfg.Obs.PersistSampling, logger)
	chat := service.NewChatService(cfg, mainLLM, miniLLM, cpStore, msg, metrics, logger)

	// Artifact tools (publish/get a live shareable page) are local tools with DB
	// access; the providers append them to every run's tool set.
	service.ArtifactTools = service.BuildArtifactTools(store)
	// Quant factor-pipeline tools (validate/backtest/agreement/ingest/portfolio),
	// bound to the workspace storage root.
	service.QuantTools = service.BuildQuantTools(cfg.Storage.BaseStoragePath)

	// Load Claude-Code-style SKILL.md packages from the global skills dir, merged
	// over the built-in catalog; skill_create writes new ones here.
	if n, err := middlewares.LoadSkills(cfg.Agent.SkillsDir); err != nil {
		logger.Warn("skills load failed", "dir", cfg.Agent.SkillsDir, "err", err)
	} else if n > 0 {
		logger.Info("skills loaded", "dir", cfg.Agent.SkillsDir, "count", n)
	}

	// Wire the real GUI executor (run_agent) when configured; else keep the mock.
	if wsURL := cfg.Agent.GUIAgentWSURL; wsURL != "" {
		guiToken := os.Getenv("GUI_AUTH_TOKEN")
		service.GUITool = connectors.NewRunAgentTool(wsURL, guiToken)
		if guiToken == "" {
			logger.Warn("gui agent has no GUI_AUTH_TOKEN; the executor is unauthenticated (dev only)")
		}
		logger.Info("gui agent enabled", "ws", wsURL)
	}

	// Optionally source tools from a remote tools_server over MCP. When unset,
	// the control layer uses the local filesystem tools directly.
	if toolsURL := os.Getenv("TOOLS_MCP_URL"); toolsURL != "" {
		ttl := time.Duration(cfg.Security.CtxTokenTTLSec) * time.Second
		prov, inval := service.MCPToolsProviderPooled(
			cfg.Storage.BaseStoragePath, toolsURL, cfg.Security.CtxTokenSecret,
			ttl, []string{"file:read", "file:write", "web:search"},
			func(ctx context.Context, email string) ([]db.Connector, error) {
				return store.EnabledConnectors(ctx, email)
			},
		)
		chat.ToolsFor = prov
		chat.InvalidateTools = inval
		waitReady(toolsURL, 15*time.Second, logger) // avoid first-request startup race
		logger.Info("tools via MCP", "url", toolsURL)
	}

	logger.Info("control layer starting",
		"addr", cfg.Server.ControlAddr,
		"mongo", cfg.Storage.MongoDB,
		"redis", cfg.Storage.RedisAddr,
		"runtime", "eino",
		"cors_hosts", cfg.Server.CORSAllowedHosts,
	)

	a := api.New(store, logger, metrics, chat)
	// Push per-user UI-invalidation events (run finished / unattended failure) to
	// open tabs via the API event bus, so the bell/runs refresh without polling.
	chat.OnEvent = a.PublishEvent
	a.BaseStorage = cfg.Storage.BaseStoragePath
	a.SalesBIReportRoot = os.Getenv("SALES_REPORT_ROOT")
	if a.SalesBIReportRoot == "" {
		if home, err := os.UserHomeDir(); err == nil {
			a.SalesBIReportRoot = filepath.Join(home, ".qwenpaw", "workspaces", "default", "reports", "sales")
		}
	}
	// User session tokens are signed with a key DISTINCT from the control<->tools
	// context-token key, so the two token types are not interchangeable.
	a.Secret = cfg.SessionSecret()
	a.Directory = connectors.NewCachedDirectory(
		&connectors.StubDirectory{},
		state.NewRedisStore(rdb, "orka:dir:"),
		time.Hour,
	)

	// Optional cron scheduler: render task templates and trigger chat runs.
	if os.Getenv("SCHEDULER_ENABLE") == "1" {
		sched := &scheduled_task.Scheduler{
			Source: scheduled_task.CronSource(store),
			Trigger: func(ctx context.Context, task db.TaskMeta, content string) error {
				// Dispatch asynchronously: a scheduled run can take minutes, and the
				// scheduler has already claimed the task (advanced next_run_at), so
				// the loop must not block on it — otherwise other due tasks wait.
				go chat.RunHeadless(context.Background(), service.ChatRunRequest{
					Message: content, ConversationID: task.ConversationID,
					TaskID: task.TaskID, UserEmail: task.OwnerEmail,
					Trigger: "schedule",
				})
				return nil
			},
			Advance: func(ctx context.Context, taskID string, nextRunAt int64) error {
				return store.AdvanceTaskRun(ctx, taskID, nextRunAt, "")
			},
			Interval: 20 * time.Second,
			Log:      logger,
		}
		go sched.Start(context.Background())
		logger.Info("scheduler enabled")
	}

	h := server.New(server.WithHostPorts(cfg.Server.ControlAddr))
	registerRoutes(h, a, cfg.Server.CORSAllowedHosts)
	h.Spin()
}
