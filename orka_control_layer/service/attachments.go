package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_core/pathsafe"
)

// Attachments are processed into separate, compressible runtime context, so they
// flow through the normal (text) agent loop on either runtime:
//   - text/code files: bounded previews point to the complete session files.
//   - images: a one-shot VLM "describe + extract" pre-pass turns them into text
//     (real vision via VLM_MODEL) which is then injected. This avoids threading
//     multimodal messages through the whole tool loop while still letting the
//     agent reason about image content and use tools on it.

var imageExts = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp",
}

const (
	maxAttachText     = 20000 // shared content budget across all text attachments
	maxTabularPreview = 2048  // sample only; calculations must read the complete file
)

// withAttachments preserves the authenticated human request verbatim. Attachment
// previews can be summarized without turning file contents into permanent rules.
func (s *ChatService) withAttachments(ctx context.Context, input []messages.Message, req ChatRunRequest, meta messages.Meta) []messages.Message {
	if extra := s.processAttachments(ctx, req); extra != "" {
		m := messages.Chat(messages.RoleUser, extra, meta)
		m.Action = runtimeContextAction
		input = append(input, m)
	}
	return input
}

// processAttachments resolves req.FileIDs under the user's workspace and returns
// separate runtime context ("" if none/unavailable).
func (s *ChatService) processAttachments(ctx context.Context, req ChatRunRequest) string {
	if len(req.FileIDs) == 0 {
		return ""
	}
	root, err := pathsafe.SessionRoot(s.Cfg.Storage.BaseStoragePath, req.UserEmail, req.ConversationID)
	if err != nil {
		return ""
	}
	remaining := maxAttachText
	var out strings.Builder
	var imgURLs, imgNames []string
	for _, f := range req.FileIDs {
		p, err := pathsafe.Resolve(root, f)
		if err != nil {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f))
		if mime, ok := imageExts[ext]; ok {
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			imgURLs = append(imgURLs, "data:"+mime+";base64,"+base64.StdEncoding.EncodeToString(b))
			imgNames = append(imgNames, f)
			continue
		}
		tabular := ext == ".csv" || ext == ".tsv"
		limit := remaining
		if tabular && limit > maxTabularPreview {
			limit = maxTabularPreview
		}
		content, truncated, err := attachmentPreview(p, limit)
		if err != nil {
			continue
		}
		remaining -= len(content)
		fmt.Fprintf(&out, "\n\n[附件 %s；完整文件位于当前会话工作区的此相对路径]\n```\n%s\n```", f, content)
		if tabular {
			out.WriteString("\n使用脚本读取完整表格并计算、验证；不要在思考中逐行手算，或仅为查看数据而把整张表反复载入模型上下文。")
		}
		if truncated {
			out.WriteString("\n（仅预览，内容已截断；分析和计算必须读取完整文件，不能用样例代替全量数据。）")
		}
	}
	if len(imgURLs) > 0 {
		if desc := s.describeImages(ctx, req.Message, imgURLs); desc != "" {
			fmt.Fprintf(&out, "\n\n[图片附件 %s — 已由视觉模型解析]\n%s", strings.Join(imgNames, ", "), desc)
		}
	}
	return out.String()
}

// attachmentPreview reads at most limit+1 bytes even for very large files.
func attachmentPreview(path string, limit int) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return "", false, err
	}
	truncated := len(b) > limit
	if truncated {
		b = b[:limit]
	}
	// Drop invalid bytes, including a partial final rune, without expanding the budget.
	return strings.ToValidUTF8(string(b), ""), truncated, nil
}

// describeImages runs a single VLM call to extract everything relevant from the
// attached images, given the user's request as guidance.
func (s *ChatService) describeImages(ctx context.Context, userText string, urls []string) string {
	models := s.modelsForContext(ctx)
	vlm := models.cfg.VLMModel
	if vlm == "" || models.client == nil {
		return "(未配置视觉模型，无法解析图片)"
	}
	prompt := "Describe the attached image(s) in detail and extract ALL text, data, and elements relevant to the user's request. " +
		"Be thorough and literal — this description is the only way a downstream text-only agent can 'see' the image.\n\nUser's request: " + userText
	resp, err := models.client.Chat(llm.WithAgent(ctx, "attachment-vlm"), boundedDirectRequest(ctx, llm.Request{
		Model:    vlm,
		Messages: []llm.ChatMessage{{Role: llm.RoleUser, Content: prompt, Images: urls}},
	}))
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
