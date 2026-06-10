// Command echoserver runs the echo MCP server over stdio (test fixture).
package main

import (
	"log"

	"github.com/mark3labs/mcp-go/server"

	"github.com/orka-oss/orka_middleware/internal/echo"
)

func main() {
	if err := server.ServeStdio(echo.Server()); err != nil {
		log.Fatalf("serve stdio: %v", err)
	}
}
