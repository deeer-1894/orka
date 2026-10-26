package api

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"golang.org/x/crypto/bcrypt"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_core/security"
)

const sessionTTL = 7 * 24 * time.Hour

type authReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

// mediaTTL bounds the download-only token that goes into URLs (href, img src):
// a URL ends up in access logs and browser history, so what it carries must
// expire soon and must not work as a session.
const mediaTTL = 4 * time.Hour

const (
	scopeSession = "session"
	scopeMedia   = "media"
)

func (a *API) issueToken(ctx context.Context, email string) (string, error) {
	return a.sign(ctx, email, scopeSession, sessionTTL)
}

func (a *API) sign(ctx context.Context, email, scope string, ttl time.Duration) (string, error) {
	t := security.NewToken(email, []string{scope}, ttl)
	t.Version = a.tokenVersion(ctx, email)
	return security.Sign(t, []byte(a.Secret))
}

// tokenVersion is the account's current token version, cached briefly so the
// middleware does not read Mongo on every request. A revocation reaches other
// instances within tokenVersionTTL.
const tokenVersionTTL = 30 * time.Second

type cachedVersion struct {
	v  int64
	at time.Time
}

func (a *API) tokenVersion(ctx context.Context, email string) int64 {
	a.versionsMu.Lock()
	cv, ok := a.versions[email]
	a.versionsMu.Unlock()
	if ok && time.Since(cv.at) < tokenVersionTTL {
		return cv.v
	}
	v := int64(0)
	if a.Store != nil && a.Store.Users != nil {
		if u, err := a.Store.GetUser(ctx, email); err == nil {
			v = u.TokenVersion
		}
	}
	a.rememberVersion(email, v)
	return v
}

func (a *API) rememberVersion(email string, v int64) {
	a.versionsMu.Lock()
	if a.versions == nil {
		a.versions = map[string]cachedVersion{}
	}
	a.versions[email] = cachedVersion{v, time.Now()}
	a.versionsMu.Unlock()
}

// validPassword is the minimum for a NEW password: 8 characters with both a
// letter and a digit. Existing passwords keep working until changed.
func validPassword(p string) error {
	hasLetter, hasDigit := false, false
	for _, r := range p {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case unicode.IsLetter(r):
			hasLetter = true
		}
	}
	if len([]rune(p)) < 8 || !hasLetter || !hasDigit {
		return errors.New("密码至少 8 位，并且同时包含字母和数字")
	}
	return nil
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
	if req.Email == "" {
		fail(c, consts.StatusBadRequest, "email required")
		return
	}
	if err := validPassword(req.Password); err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
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
	tok, _ := a.issueToken(ctx, req.Email)
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
	tok, _ := a.issueToken(ctx, u.Email)
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
	ok(c, map[string]any{"email": u.Email, "name": u.Name, "onboarding": u.Onboarding})
}

// MarkOnboarding records a first-run guide step (see db.Onboarding*).
func (a *API) MarkOnboarding(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Step string `json:"step"`
	}
	if err := bind(c, &req); err != nil || (req.Step != db.OnboardingTour && req.Step != db.OnboardingDismissed) {
		fail(c, consts.StatusBadRequest, "unknown onboarding step")
		return
	}
	if err := a.Store.MarkOnboarding(ctx, authEmail(c), req.Step); err != nil {
		fail(c, consts.StatusInternalServerError, "保存失败")
		return
	}
	ok(c, map[string]any{"step": req.Step})
}

// Logout revokes every token the caller holds, on every device.
func (a *API) Logout(ctx context.Context, c *app.RequestContext) {
	email := authEmail(c)
	v, err := a.Store.BumpTokenVersion(ctx, email)
	if err != nil {
		fail(c, consts.StatusInternalServerError, "logout failed")
		return
	}
	a.rememberVersion(email, v)
	ok(c, map[string]bool{"logged_out": true})
}

// ChangePassword checks the current password, sets the new one, revokes every
// existing token and returns a fresh session for this device.
func (a *API) ChangePassword(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Old string `json:"old_password"`
		New string `json:"new_password"`
	}
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request")
		return
	}
	email := authEmail(c)
	if a.authLimiter != nil && !a.authLimiter.allow("password:"+email) {
		fail(c, consts.StatusTooManyRequests, "too many attempts, please try again later")
		return
	}
	u, err := a.Store.GetUser(ctx, email)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Old)) != nil {
		fail(c, consts.StatusUnauthorized, "当前密码不正确")
		return
	}
	if err := validPassword(req.New); err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.New), bcrypt.DefaultCost)
	if err != nil {
		fail(c, consts.StatusInternalServerError, "hash failed")
		return
	}
	v, err := a.Store.SetPassword(ctx, email, string(hash))
	if err != nil {
		fail(c, consts.StatusInternalServerError, "save failed")
		return
	}
	a.rememberVersion(email, v)
	tok, _ := a.issueToken(ctx, email)
	ok(c, map[string]string{"token": tok})
}

// MediaToken issues the short-lived, GET-only token download links carry.
func (a *API) MediaToken(ctx context.Context, c *app.RequestContext) {
	tok, err := a.sign(ctx, authEmail(c), scopeMedia, mediaTTL)
	if err != nil {
		fail(c, consts.StatusInternalServerError, "token failed")
		return
	}
	ok(c, map[string]any{"token": tok, "expires_at": time.Now().Add(mediaTTL).UnixMilli()})
}

// AuthMiddleware verifies the Bearer session token and sets the user email.
func (a *API) AuthMiddleware() app.HandlerFunc {
	secret := []byte(a.Secret)
	return func(ctx context.Context, c *app.RequestContext) {
		// A session travels only in the Authorization header. A URL (an <a
		// href> download, an <img src>) carries a short-lived media token that
		// works for GET only — URLs land in logs and browser history.
		tok, scope := strings.TrimPrefix(string(c.GetHeader("Authorization")), "Bearer "), scopeSession
		if tok == "" {
			tok, scope = string(c.Query("token")), scopeMedia
		}
		if tok == "" {
			fail(c, consts.StatusUnauthorized, "missing token")
			c.Abort()
			return
		}
		claims, err := security.Verify(tok, secret)
		if err == nil && (!slices.Contains(claims.Scopes, scope) || (scope == scopeMedia && string(c.Method()) != "GET")) {
			err = errors.New("wrong token kind")
		}
		if err == nil && claims.Version != a.tokenVersion(ctx, claims.UserEmail) {
			err = errors.New("token revoked")
		}
		if err != nil {
			fail(c, consts.StatusUnauthorized, "invalid or expired token")
			c.Abort()
			return
		}
		c.Set("email", claims.UserEmail)
		c.Next(ctx)
	}
}
