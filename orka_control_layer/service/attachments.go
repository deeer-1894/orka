package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/orka-oss/orka_core/pathsafe"
	"github.com/orka-oss/orka_control_layer/llm"
)

// Attachments are processed into TEXT that augments the user's message, so they
// flow through the normal (text) agent loop on either runtime:
//   - text/code files: their content is injected inline.
//   - images: a one-shot VLM "describe + extract" pre-pass turns them into text
//     (real vision via VLM_MODEL) which is then injected. This avoids threading
//     multimodal messages through the whole tool loop while still letting the
//     agent reason about image content and use tools on it.

var imageExts = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp",
}

const maxAttachText = 20000 // cap injected text-file size to protect the context

// processAttachments resolves req.FileIDs under the user's workspace and returns
// extra context to append to the user message ("" if none/unavailable).
func (s *ChatService) processAttachments(ctx context.Context, req ChatRunRequest) string {
	if len(req.FileIDs) == 0 {
		return ""
	}
	root := pathsafe.UserRoot(s.Cfg.Storage.BaseStoragePath, req.UserEmail)
	var out strings.Builder
	var imgURLs, imgNames []string
	for _, f := range req.FileIDs {
		p, err := pathsafe.Resolve(root, f)
		if err != nil {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if mime, ok := imageExts[strings.ToLower(filepath.Ext(f))]; ok {
			imgURLs = append(imgURLs, "data:"+mime+";base64,"+base64.StdEncoding.EncodeToString(b))
			imgNames = append(imgNames, f)
			continue
		}
		content := string(b)
		if len(content) > maxAttachText {
			content = content[:maxAttachText] + "\n…(truncated)"
		}
		fmt.Fprintf(&out, "\n\n[附件 %s]\n```\n%s\n```", f, content)
	}
	if len(imgURLs) > 0 {
		if desc := s.describeImages(ctx, req.Message, imgURLs); desc != "" {
			fmt.Fprintf(&out, "\n\n[图片附件 %s — 已由视觉模型解析]\n%s", strings.Join(imgNames, ", "), desc)
		}
	}
	return out.String()
}

// describeImages runs a single VLM call to extract everything relevant from the
// attached images, given the user's request as guidance.
func (s *ChatService) describeImages(ctx context.Context, userText string, urls []string) string {
	vlm := s.Cfg.LLM.VLMModel
	if vlm == "" || s.Main == nil {
		return "(未配置视觉模型，无法解析图片)"
	}
	prompt := "Describe the attached image(s) in detail and extract ALL text, data, and elements relevant to the user's request. " +
		"Be thorough and literal — this description is the only way a downstream text-only agent can 'see' the image.\n\nUser's request: " + userText
	resp, err := s.Main.Chat(llm.WithAgent(ctx, "attachment-vlm"), llm.Request{
		Model:    vlm,
		Messages: []llm.ChatMessage{{Role: llm.RoleUser, Content: prompt, Images: urls}},
	})
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("vlm image describe failed", "err", err)
		}
		// Clean degradation: tell the agent (and thus the user) plainly, without
		// leaking the raw upstream error. Most often the configured endpoint just
		// doesn't accept image input (needs a real vision model).
		return "(无法解析图片：当前配置的视觉模型不可用或不支持图像输入。请改用文本，或配置一个支持视觉的 VLM_MODEL。)"
	}
	return strings.TrimSpace(resp.Content)
}
