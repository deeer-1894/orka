package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_control_layer/db"
)

// runInfo carries the active conversation + owner down to local tools, which
// (unlike the gateway's signed-token tools) have no other way to learn whose
// run they're in. Run() installs it before invoking the agent.
type runInfoKey struct{}
type runInfo struct{ conv, owner string }

// WithRunInfo tags ctx with the conversation id + owner for local tools.
func WithRunInfo(ctx context.Context, conv, owner string) context.Context {
	return context.WithValue(ctx, runInfoKey{}, runInfo{conv: conv, owner: owner})
}
func runInfoFrom(ctx context.Context) (conv, owner string) {
	if ri, ok := ctx.Value(runInfoKey{}).(runInfo); ok {
		return ri.conv, ri.owner
	}
	return "", ""
}

// ArtifactTools is the set of artifact tools, wired by main with a store. Empty
// until then; the providers append it like the skill tools.
var ArtifactTools []agent.BaseTool

// BuildArtifactTools constructs the publish/get tools bound to the store.
func BuildArtifactTools(store *db.Storage) []agent.BaseTool {
	return []agent.BaseTool{&artifactPublishTool{store: store}, &artifactGetTool{store: store}}
}

// ---- artifact_publish ----

type artifactPublishTool struct{ store *db.Storage }

func (*artifactPublishTool) Name() string { return "artifact_publish" }
func (*artifactPublishTool) Description() string {
	return "Publish or update a live, shareable Artifact page for this conversation — a visual summary that the viewer sees refresh in place as you work. Call it again to update; each call is a new version. Args: title, kind (pr_review|architecture|incident|checklist|audit|custom), blocks (an array of typed content blocks), note (what changed). " + artifactBlocksDoc
}
func (*artifactPublishTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":  map[string]any{"type": "string", "description": "page title"},
			"kind":   map[string]any{"type": "string", "description": "pr_review|architecture|incident|checklist|audit|custom"},
			"note":   map[string]any{"type": "string", "description": "what changed this update"},
			"blocks": map[string]any{"type": "array", "description": "ordered content blocks, each {type, data}", "items": map[string]any{"type": "object"}},
		},
		"required": []string{"title", "blocks"},
	}
}
func (t *artifactPublishTool) Invoke(ctx context.Context, args map[string]any) (string, error) {
	conv, owner := runInfoFrom(ctx)
	if conv == "" || owner == "" {
		return "", fmt.Errorf("artifact_publish: no active conversation context")
	}
	title := strings.TrimSpace(asStr(args["title"]))
	if title == "" {
		title = "Untitled Artifact"
	}
	blocks, err := parseBlocks(args["blocks"])
	if err != nil {
		return "", fmt.Errorf("artifact_publish: %w", err)
	}

	art, err := t.store.GetArtifactByConversation(ctx, conv)
	if err == db.ErrNotFound {
		art = &db.Artifact{
			ArtifactID:     "art_" + randHex(8),
			OwnerEmail:     owner,
			ConversationID: conv,
			Title:          title,
			Kind:           strings.TrimSpace(asStr(args["kind"])),
			Slug:           slugify(title) + "-" + randHex(4),
			Visibility:     db.ArtifactPrivate,
			CreatedAt:      time.Now().UnixMilli(),
			UpdatedAt:      time.Now().UnixMilli(),
		}
		if err := t.store.CreateArtifact(ctx, art); err != nil {
			return "", fmt.Errorf("artifact_publish: %w", err)
		}
	} else if err != nil {
		return "", fmt.Errorf("artifact_publish: %w", err)
	} else {
		art.Title = title // keep title in sync with the latest publish
		_ = t.store.UpdateArtifactTitle(ctx, art.ArtifactID, title)
	}

	v, err := t.store.AppendArtifactVersion(ctx, art.ArtifactID, blocks, strings.TrimSpace(asStr(args["note"])))
	if err != nil {
		return "", fmt.Errorf("artifact_publish: %w", err)
	}
	ArtifactHub.Publish(art.ArtifactID, v)
	return fmt.Sprintf("Published Artifact %q (v%d) — open it at /a/%s", title, v, art.Slug), nil
}

// ---- artifact_get ----

type artifactGetTool struct{ store *db.Storage }

func (*artifactGetTool) Name() string { return "artifact_get" }
func (*artifactGetTool) Description() string {
	return "Return the current Artifact for this conversation (its title + blocks) so you can update it incrementally instead of rebuilding from scratch. Returns nothing if none exists yet."
}
func (*artifactGetTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *artifactGetTool) Invoke(ctx context.Context, _ map[string]any) (string, error) {
	conv, _ := runInfoFrom(ctx)
	if conv == "" {
		return "", fmt.Errorf("artifact_get: no active conversation context")
	}
	art, err := t.store.GetArtifactByConversation(ctx, conv)
	if err == db.ErrNotFound {
		return "(no artifact yet for this conversation)", nil
	}
	if err != nil {
		return "", err
	}
	ver, err := t.store.GetArtifactVersion(ctx, art.ArtifactID, 0)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{"title": art.Title, "kind": art.Kind, "version": ver.Version, "blocks": ver.Blocks})
	return string(out), nil
}

// ---- helpers ----

const artifactBlocksDoc = `Block types and their data: ` +
	`markdown {text}; heading {text, level}; ` +
	`table {columns:[..], rows:[[..],..]}; ` +
	`checklist {items:[{label, status: done|doing|todo|blocked}]}; ` +
	`metric {label, value, delta}; diff {path, patch (unified diff text)}; ` +
	`timeline {events:[{time, title, detail}]}; code {language, text}; ` +
	`badge {label, tone: ok|warn|danger|info}.`

func parseBlocks(v any) ([]db.ArtifactBlock, error) {
	if v == nil {
		return nil, fmt.Errorf("blocks is required")
	}
	// The model may pass a JSON string or an already-decoded slice; normalize
	// by round-tripping through JSON.
	var raw []byte
	switch x := v.(type) {
	case string:
		raw = []byte(x)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	var blocks []db.ArtifactBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("blocks must be a JSON array of {type, data}: %w", err)
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("blocks is empty")
	}
	return blocks, nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		s = "artifact"
	}
	return s
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
