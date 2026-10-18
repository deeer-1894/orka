package main

import (
	"errors"
	"net/url"
)

// browserEndpoint shares the configured GUI origin, never a model-supplied or
// root Chromium endpoint. The existing GUI auth token is passed separately.
func browserEndpoint(guiURL string) (string, error) {
	u, err := url.Parse(guiURL)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "ws" && u.Scheme != "wss") {
		return "", errors.New("invalid GUI WebSocket origin")
	}
	u.Path = "/api/v1/browser/cdp/ws"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
