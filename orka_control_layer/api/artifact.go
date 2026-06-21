package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"strconv"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/service"
)

// ListArtifacts returns the caller's artifacts for the gallery (newest first).
func (a *API) ListArtifacts(ctx context.Context, c *app.RequestContext) {
	arts, err := a.Store.ListArtifacts(ctx, authEmail(c))
	if err != nil {
		fail(c, consts.StatusInternalServerError, "list failed")
		return
	}
	ok(c, map[string]any{"artifacts": arts})
}

type artifactGetReq struct {
	ArtifactID string `json:"artifact_id"`
	Slug       string `json:"slug"`
	Version    int    `json:"version"`
}

// GetArtifact returns an artifact + one version's blocks (authed: owner/shared).
func (a *API) GetArtifact(ctx context.Context, c *app.RequestContext) {
	var req artifactGetReq
	if err := bind(c, &req); err != nil || (req.ArtifactID == "" && req.Slug == "") {
		fail(c, consts.StatusBadRequest, "artifact_id or slug required")
		return
	}
	art := a.lookupArtifact(ctx, req.ArtifactID, req.Slug)
	if art == nil || !art.CanRead(authEmail(c)) {
		fail(c, consts.StatusNotFound, "not found")
		return
	}
	a.respondArtifact(ctx, c, art, req.Version)
}

// ArtifactVersions lists a artifact's version history (no blocks).
func (a *API) ArtifactVersions(ctx context.Context, c *app.RequestContext) {
	var req artifactGetReq
	if err := bind(c, &req); err != nil || req.ArtifactID == "" {
		fail(c, consts.StatusBadRequest, "artifact_id required")
		return
	}
	art, err := a.Store.GetArtifact(ctx, req.ArtifactID)
	if err != nil || !art.CanRead(authEmail(c)) {
		fail(c, consts.StatusNotFound, "not found")
		return
	}
	vers, err := a.Store.ListArtifactVersions(ctx, req.ArtifactID)
	if err != nil {
		fail(c, consts.StatusInternalServerError, "list failed")
		return
	}
	ok(c, vers)
}

// ShareArtifact grants/revokes per-user access (owner only) — reuses the
// conversation share roles.
func (a *API) ShareArtifact(ctx context.Context, c *app.RequestContext) {
	var req shareReq
	if err := bind(c, &req); err != nil || req.ConversationID == "" || req.Email == "" {
		fail(c, consts.StatusBadRequest, "artifact_id (conversation_id field) and email required")
		return
	}
	art, err := a.Store.GetArtifact(ctx, req.ConversationID) // reuse field as artifact_id
	if err != nil || art.OwnerEmail != authEmail(c) {
		fail(c, consts.StatusNotFound, "not found")
		return
	}
	next := make([]db.ConversationShare, 0, len(art.Shares)+1)
	for _, s := range art.Shares {
		if s.Email != req.Email {
			next = append(next, s)
		}
	}
	switch req.Role {
	case db.RoleViewer, db.RoleEditor:
		next = append(next, db.ConversationShare{Email: req.Email, Role: req.Role})
	case "", "none", "remove":
	default:
		fail(c, consts.StatusBadRequest, "role must be viewer, editor, or none")
		return
	}
	if err := a.Store.UpdateArtifactShares(ctx, art.ArtifactID, next); err != nil {
		fail(c, consts.StatusInternalServerError, "share failed")
		return
	}
	vis := art.Visibility
	if len(next) > 0 && vis == db.ArtifactPrivate {
		vis = db.ArtifactShared
		_ = a.Store.SetArtifactVisibility(ctx, art.ArtifactID, vis, art.ShareToken)
	}
	ok(c, map[string]any{"artifact_id": art.ArtifactID, "shares": next, "visibility": vis})
}

type visibilityReq struct {
	ArtifactID string `json:"artifact_id"`
	Public     bool   `json:"public"`
}

// SetArtifactVisibility toggles the public share link (owner only). Making an
// artifact public is an explicit owner action — the agent never does this.
func (a *API) SetArtifactVisibility(ctx context.Context, c *app.RequestContext) {
	var req visibilityReq
	if err := bind(c, &req); err != nil || req.ArtifactID == "" {
		fail(c, consts.StatusBadRequest, "artifact_id required")
		return
	}
	art, err := a.Store.GetArtifact(ctx, req.ArtifactID)
	if err != nil || art.OwnerEmail != authEmail(c) {
		fail(c, consts.StatusNotFound, "not found")
		return
	}
	vis, token := db.ArtifactPrivate, ""
	if req.Public {
		vis = db.ArtifactPublic
		token = art.ShareToken
		if token == "" {
			token = randToken()
		}
	} else if len(art.Shares) > 0 {
		vis = db.ArtifactShared // keep per-user shares when revoking the public link
	}
	if err := a.Store.SetArtifactVisibility(ctx, art.ArtifactID, vis, token); err != nil {
		fail(c, consts.StatusInternalServerError, "update failed")
		return
	}
	ok(c, map[string]any{"visibility": vis, "share_token": token, "slug": art.Slug})
}

// DeleteArtifact removes an artifact (owner only).
func (a *API) DeleteArtifact(ctx context.Context, c *app.RequestContext) {
	var req artifactGetReq
	if err := bind(c, &req); err != nil || req.ArtifactID == "" {
		fail(c, consts.StatusBadRequest, "artifact_id required")
		return
	}
	if err := a.Store.DeleteArtifact(ctx, req.ArtifactID, authEmail(c)); err != nil {
		fail(c, consts.StatusInternalServerError, "delete failed")
		return
	}
	ok(c, map[string]string{"deleted": req.ArtifactID})
}

// ArtifactStream is an SSE feed of version bumps for an artifact (authed).
func (a *API) ArtifactStream(ctx context.Context, c *app.RequestContext) {
	art, err := a.Store.GetArtifact(ctx, string(c.Query("artifact_id")))
	if err != nil || !art.CanRead(authEmail(c)) {
		fail(c, consts.StatusNotFound, "not found")
		return
	}
	a.streamArtifact(c, art.ArtifactID, art.CurrentVersion)
}

// ---- public (no auth; the share token is the auth, like /hook/:token) ----

// GetPublicArtifact serves a public artifact by slug + token.
func (a *API) GetPublicArtifact(ctx context.Context, c *app.RequestContext) {
	art := a.publicArtifact(ctx, c)
	if art == nil {
		return
	}
	v, _ := strconv.Atoi(string(c.Query("v")))
	a.respondArtifact(ctx, c, art, v)
}

// PublicArtifactStream is the public SSE feed (slug + token).
func (a *API) PublicArtifactStream(ctx context.Context, c *app.RequestContext) {
	art := a.publicArtifact(ctx, c)
	if art == nil {
		return
	}
	a.streamArtifact(c, art.ArtifactID, art.CurrentVersion)
}

// publicArtifact validates a public slug+token, or writes 404 and returns nil.
func (a *API) publicArtifact(ctx context.Context, c *app.RequestContext) *db.Artifact {
	art, err := a.Store.GetArtifactBySlug(ctx, c.Param("slug"))
	if err != nil || art.Visibility != db.ArtifactPublic || art.ShareToken == "" ||
		string(c.Query("token")) != art.ShareToken {
		fail(c, consts.StatusNotFound, "not found")
		return nil
	}
	return art
}

// ---- shared helpers ----

func (a *API) lookupArtifact(ctx context.Context, id, slug string) *db.Artifact {
	if id != "" {
		if art, err := a.Store.GetArtifact(ctx, id); err == nil {
			return art
		}
		return nil
	}
	art, err := a.Store.GetArtifactBySlug(ctx, slug)
	if err != nil {
		return nil
	}
	return art
}

func (a *API) respondArtifact(ctx context.Context, c *app.RequestContext, art *db.Artifact, version int) {
	ver, err := a.Store.GetArtifactVersion(ctx, art.ArtifactID, version)
	if err != nil {
		fail(c, consts.StatusNotFound, "version not found")
		return
	}
	ok(c, map[string]any{"artifact": art, "version": ver})
}

// streamArtifact writes an SSE stream: an initial version frame, then a frame
// per published bump, until the client disconnects.
func (a *API) streamArtifact(c *app.RequestContext, artifactID string, current int) {
	sub, cancel := service.ArtifactHub.Subscribe(artifactID)
	c.SetStatusCode(consts.StatusOK)
	c.Response.Header.Set("Content-Type", "text/event-stream")
	c.Response.Header.Set("Cache-Control", "no-cache")
	c.Response.Header.Set("Connection", "keep-alive")
	pr, pw := io.Pipe()
	c.SetBodyStream(pr, -1)
	go func() {
		defer pw.Close()
		defer cancel()
		writeSSE(pw, current) // tell a late joiner the current version immediately
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case v, okk := <-sub:
				if !okk {
					return
				}
				if !writeSSE(pw, v) {
					return
				}
			case <-ticker.C:
				if _, err := pw.Write([]byte(": ping\n\n")); err != nil { // keep-alive comment
					return
				}
			}
		}
	}()
}

// writeSSE emits one "data: <version>" SSE frame; returns false if the client
// has gone (write error), ending the stream.
func writeSSE(pw *io.PipeWriter, version int) bool {
	_, err := pw.Write([]byte("data: " + strconv.Itoa(version) + "\n\n"))
	return err == nil
}

func randToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
