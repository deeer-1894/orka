// Package modelsettings keeps user-owned provider credentials outside agent workspaces.
package modelsettings

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var ErrStorage = errors.New("model settings storage unavailable")
var ErrOwner = errors.New("authenticated owner required")

const maxResponseBytes = 1 << 20

// Config is an internal snapshot. APIKey cannot be serialized by callers.
type Config struct {
	Provider  string   `json:"provider"`
	BaseURL   string   `json:"base_url"`
	APIKey    string   `json:"-"`
	Model     string   `json:"-"` // legacy input only; normalized into Models
	MiniModel string   `json:"-"` // legacy input only; never a separate tier
	Models    []string `json:"models"`
	Enabled   bool     `json:"enabled"`
}

type PublicConfig struct {
	Config
	APIKeySet bool `json:"api_key_set"`
}

func (c Config) Public() PublicConfig {
	hasKey := c.APIKey != ""
	c.APIKey = ""
	c.Models = append([]string{}, c.Models...)
	return PublicConfig{Config: c, APIKeySet: hasKey}
}

type diskConfig struct {
	Config
	Key         string `json:"api_key,omitempty"`
	LegacyModel string `json:"model,omitempty"`
}

type Store struct {
	dir string
	mu  sync.Mutex
}

// New does not create directories until the first save. The base must be the
// deployment storage base, never a user or conversation workspace.
func New(base string) *Store {
	if base == "" {
		return &Store{}
	}
	return &Store{dir: filepath.Join(filepath.Dir(filepath.Clean(base)), "model-settings")}
}

func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || strings.ContainsAny(raw, "?#") || u.Opaque != "" {
		return "", errors.New("base_url must be an http(s) URL without credentials, query or fragment")
	}
	u.Host = strings.ToLower(u.Host)
	return strings.TrimRight(u.String(), "/"), nil
}

func validText(s string, max int) bool { return len(s) <= max && !strings.ContainsAny(s, "\r\n\x00") }

func normalize(c Config) (Config, error) {
	c.Provider = strings.TrimSpace(c.Provider)
	if c.Provider == "" {
		c.Provider = "openai-compatible"
	}
	if !validText(c.Provider, 128) || len(c.Models) > 2048 {
		return Config{}, errors.New("invalid provider or model")
	}
	if c.BaseURL != "" || c.Enabled {
		var err error
		c.BaseURL, err = NormalizeURL(c.BaseURL)
		if err != nil {
			return Config{}, err
		}
	}

	c.Models = OrderedModels(c.Model, c.Models)
	for _, m := range c.Models {
		if !validText(m, 256) {
			return Config{}, errors.New("invalid model")
		}
	}
	if len(c.Models) > 2048 {
		return Config{}, errors.New("too many models")
	}
	if c.Enabled && len(c.Models) == 0 {
		return Config{}, errors.New("models must contain at least one model when enabled")
	}
	c.Model, c.MiniModel = "", ""
	return c, nil
}

// OrderedModels migrates the legacy default followed by the explicit list.
// The legacy mini field is deliberately excluded; listed names have no tier.
func OrderedModels(legacyDefault string, models []string) []string {
	out := make([]string, 0, len(models)+1)
	seen := map[string]bool{}
	for _, m := range append([]string{legacyDefault}, models...) {
		m = strings.TrimSpace(m)
		if m != "" && !seen[m] {
			out = append(out, m)
			seen[m] = true
		}
	}
	return out
}

func (s *Store) path(owner string) (string, error) {
	if strings.TrimSpace(owner) == "" {
		return "", ErrOwner
	}
	if s == nil || s.dir == "" {
		return "", ErrStorage
	}
	h := sha256.Sum256([]byte(owner))
	return filepath.Join(s.dir, hex.EncodeToString(h[:])+".json"), nil
}

// The private directory is a sibling of the mounted workspace, never inside it.
// Do not follow a pre-existing symlink.
func (s *Store) directories(create bool) error {
	for _, dir := range []string{s.dir} {
		if create {
			if err := os.MkdirAll(dir, 0700); err != nil {
				return ErrStorage
			}
		}
		info, err := os.Lstat(dir)
		if !create && os.IsNotExist(err) {
			return nil
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrStorage
		}
		if create {
			if err := os.Chmod(dir, 0700); err != nil {
				return ErrStorage
			}
		}
	}
	return nil
}

func (s *Store) get(owner string) (Config, bool, error) {
	path, err := s.path(owner)
	if err != nil {
		return Config{}, false, err
	}
	if err = s.directories(false); err != nil {
		return Config{}, false, err
	}
	st, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return Config{Provider: "openai-compatible", Models: []string{}}, false, nil
	}
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return Config{}, false, ErrStorage
	}
	f, err := os.Open(path)
	if err != nil {
		return Config{}, false, ErrStorage
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxResponseBytes+1))
	if err != nil || len(b) > maxResponseBytes {
		return Config{}, false, ErrStorage
	}
	var d diskConfig
	if json.Unmarshal(b, &d) != nil {
		return Config{}, false, ErrStorage
	}
	d.Config.Model = d.LegacyModel
	c, err := normalize(d.Config)
	if err != nil {
		return Config{}, false, ErrStorage
	}
	c.APIKey = d.Key
	return c, true, nil
}

func (s *Store) Get(owner string) (Config, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get(owner)
}

// Save atomically replaces the private file. A blank key preserves the prior
// key only for the exact normalized base URL; changing providers cannot send
// the old credential to a new endpoint. An enabled:false-only payload disables
// the override while retaining the user's editable settings.
func (s *Store) Save(owner string, c Config, key string) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prior, _, err := s.get(owner)
	if err != nil {
		return Config{}, err
	}
	if !c.Enabled && c.BaseURL == "" && c.Model == "" && len(c.Models) == 0 {
		c = prior
		c.Enabled = false
	}
	c, err = normalize(c)
	if err != nil {
		return Config{}, err
	}
	key = strings.TrimSpace(key)
	if !validText(key, 8192) {
		return Config{}, errors.New("invalid api_key")
	}
	c.APIKey = key
	if key == "" && c.BaseURL == prior.BaseURL {
		c.APIKey = prior.APIKey
	}
	if err = s.directories(true); err != nil {
		return Config{}, err
	}
	b, err := json.Marshal(diskConfig{Config: c, Key: c.APIKey})
	if err != nil {
		return Config{}, ErrStorage
	}
	f, err := os.CreateTemp(s.dir, ".settings-*")
	if err != nil {
		return Config{}, ErrStorage
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return Config{}, ErrStorage
	}
	path, _ := s.path(owner)
	if os.Rename(f.Name(), path) != nil {
		return Config{}, ErrStorage
	}
	return c, nil
}
