package mcp

import "sync"

// authCache caches per-user signed tokens so we don't re-sign on every call.
type authCache struct {
	mu sync.RWMutex
	m  map[string]string // userEmail -> token
}

// NewAuthCache returns an empty auth cache.
func NewAuthCache() *authCache { return &authCache{m: map[string]string{}} }

func (c *authCache) Get(user string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.m[user]
	return t, ok
}

func (c *authCache) Set(user, token string) {
	c.mu.Lock()
	c.m[user] = token
	c.mu.Unlock()
}

// endpointCache caches resolved upstream endpoints by logical name.
type endpointCache struct {
	mu sync.RWMutex
	m  map[string]string // name -> url
}

// NewEndpointCache returns an empty endpoint cache.
func NewEndpointCache() *endpointCache { return &endpointCache{m: map[string]string{}} }

func (c *endpointCache) Get(name string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	u, ok := c.m[name]
	return u, ok
}

func (c *endpointCache) Set(name, url string) {
	c.mu.Lock()
	c.m[name] = url
	c.mu.Unlock()
}
