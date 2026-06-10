package tools

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// base64Tool encodes/decodes UTF-8 text to/from base64.
func base64Tool() mcpserver.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text := req.GetString("text", "")
		switch strings.ToLower(req.GetString("mode", "encode")) {
		case "decode":
			b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
			if err != nil {
				return mcp.NewToolResultError("invalid base64: " + err.Error()), nil
			}
			return mcp.NewToolResultText(string(b)), nil
		default:
			return mcp.NewToolResultText(base64.StdEncoding.EncodeToString([]byte(text))), nil
		}
	}
}

// hashTool computes md5/sha1/sha256 of the input text.
func hashTool() mcpserver.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text := []byte(req.GetString("text", ""))
		switch strings.ToLower(req.GetString("algo", "sha256")) {
		case "md5":
			h := md5.Sum(text)
			return mcp.NewToolResultText(hex.EncodeToString(h[:])), nil
		case "sha1":
			h := sha1.Sum(text)
			return mcp.NewToolResultText(hex.EncodeToString(h[:])), nil
		default:
			h := sha256.Sum256(text)
			return mcp.NewToolResultText(hex.EncodeToString(h[:])), nil
		}
	}
}

// uuidTool returns a random UUID v4 (no external dependency).
func uuidTool() mcpserver.ToolHandlerFunc {
	return func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		b[6] = (b[6] & 0x0f) | 0x40 // version 4
		b[8] = (b[8] & 0x3f) | 0x80 // variant 10
		s := fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
		return mcp.NewToolResultText(s), nil
	}
}

// jsonFormatTool pretty-prints or minifies a JSON document.
func jsonFormatTool() mcpserver.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		raw := req.GetString("json", "")
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return mcp.NewToolResultError("invalid JSON: " + err.Error()), nil
		}
		var out []byte
		var err error
		if strings.ToLower(req.GetString("mode", "pretty")) == "minify" {
			out, err = json.Marshal(v)
		} else {
			out, err = json.MarshalIndent(v, "", "  ")
		}
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	}
}

// textStatsTool reports character / word / line counts for the input.
func textStatsTool() mcpserver.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text := req.GetString("text", "")
		chars := len([]rune(text))
		words := len(strings.Fields(text))
		lines := strings.Count(text, "\n")
		if len(text) > 0 {
			lines++
		}
		var cjk int
		for _, r := range text {
			if unicode.Is(unicode.Han, r) {
				cjk++
			}
		}
		out := fmt.Sprintf("characters: %d\nwords: %d\nlines: %d\nCJK characters: %d", chars, words, lines, cjk)
		return mcp.NewToolResultText(out), nil
	}
}
