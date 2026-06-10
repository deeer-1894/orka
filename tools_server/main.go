package main

import (
	"log"
	"os"
	"strings"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/cavis-oss/cavis_core/config"
	"github.com/cavis-oss/tools_server/server"
)

func main() {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "config.yaml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("config validation failed: %v", err)
	}

	var blacklist []string
	if v := os.Getenv("TOOLS_BLACKLIST"); v != "" {
		blacklist = strings.Split(v, ",")
	}

	gw := server.New(server.Config{
		Secret:      cfg.Security.CtxTokenSecret,
		BaseStorage: cfg.Storage.BaseStoragePath,
		Blacklist:   blacklist,
	})

	httpSrv := mcpserver.NewStreamableHTTPServer(gw,
		mcpserver.WithEndpointPath("/api/v1/tools/mcp"),
		mcpserver.WithHTTPContextFunc(server.ContextFunc([]byte(cfg.Security.CtxTokenSecret))),
	)

	addr := cfg.Server.ToolsAddr
	log.Printf("cavis tools_server listening on %s path=/api/v1/tools/mcp storage=%s", addr, cfg.Storage.BaseStoragePath)
	if err := httpSrv.Start(addr); err != nil {
		log.Fatalf("tools_server: %v", err)
	}
}
