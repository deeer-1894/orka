package api

import (
	"context"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"golang.org/x/crypto/bcrypt"

	"github.com/cavis-oss/cavis_core/security"
	"github.com/cavis-oss/cavis_control_layer/db"
)

const sessionTTL = 7 * 24 * time.Hour

type authReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

func (a *API) issueToken(email string) (string, error) {
	return security.Sign(security.NewToken(email, []string{"session"}, sessionTTL), []byte(a.Secret))
}

// Register creates a new account.
func (a *API) Register(ctx context.Context, c *app.RequestContext) {
	var req authReq
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request")
		return
	}
	if a.authLimiter != nil && !a.authLimiter.allow("register:"+c.ClientIP()) {
		fail(c, consts.StatusTooManyRequests, "too many attempts, please slow down")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || len(req.Password) < 6 {
		fail(c, consts.StatusBadRequest, "email required and password >= 6 chars")
		return
	}
	if _, err := a.Store.GetUser(ctx, req.Email); err == nil {
		fail(c, consts.StatusConflict, "email already registered")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		fail(c, consts.StatusInternalServerError, "hash failed")
		return
	}
	name := req.Name
	if name == "" {
		name = strings.Split(req.Email, "@")[0]
	}
	u := &db.User{Email: req.Email, Name: name, PasswordHash: string(hash), CreatedAt: time.Now().UnixMilli()}
	if err := a.Store.CreateUser(ctx, u); err != nil {
		fail(c, consts.StatusInternalServerError, "create failed")
		return
	}
	tok, _ := a.issueToken(req.Email)
	ok(c, map[string]any{"token": tok, "email": req.Email, "name": name})
}

// Login authenticates and returns a session token.
func (a *API) Login(ctx context.Context, c *app.RequestContext) {
	var req authReq
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	// Throttle by IP and by email so neither a single source nor a single
	// account can be brute-forced.
	if a.authLimiter != nil &&
		(!a.authLimiter.allow("login:"+c.ClientIP()) || !a.authLimiter.allow("login:"+req.Email)) {
		fail(c, consts.StatusTooManyRequests, "too many attempts, please try again later")
		return
	}
	u, err := a.Store.GetUser(ctx, req.Email)
	if err != nil {
		// Run a dummy compare so response timing doesn't reveal whether the
		// account exists (anti-enumeration).
		bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinv"), []byte(req.Password))
		fail(c, consts.StatusUnauthorized, "invalid credentials")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)) != nil {
		fail(c, consts.StatusUnauthorized, "invalid credentials")
		return
	}
	tok, _ := a.issueToken(u.Email)
	ok(c, map[string]any{"token": tok, "email": u.Email, "name": u.Name})
}

// Me returns the current authenticated user.
func (a *API) Me(ctx context.Context, c *app.RequestContext) {
	email := authEmail(c)
	u, err := a.Store.GetUser(ctx, email)
	if err != nil {
		ok(c, map[string]any{"email": email})
		return
	}
	ok(c, map[string]any{"email": u.Email, "name": u.Name})
}

// AuthMiddleware verifies the Bearer session token and sets the user email.
func (a *API) AuthMiddleware() app.HandlerFunc {
	secret := []byte(a.Secret)
	return func(ctx context.Context, c *app.RequestContext) {
		tok := strings.TrimPrefix(string(c.GetHeader("Authorization")), "Bearer ")
		if tok == "" {
			tok = string(c.Query("token")) // for <a href> download links
		}
		if tok == "" {
			fail(c, consts.StatusUnauthorized, "missing token")
			c.Abort()
			return
		}
		claims, err := security.Verify(tok, secret)
		if err != nil {
			fail(c, consts.StatusUnauthorized, "invalid or expired token")
			c.Abort()
			return
		}
		c.Set("email", claims.UserEmail)
		c.Next(ctx)
	}
}
