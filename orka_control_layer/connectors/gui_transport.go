package connectors

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
)

// Resolve at dial time and connect to that exact address. A later DNS change
// cannot redirect the credential-bearing handshake to a public endpoint.
func dialGUI(ctx context.Context, endpoint, token string) (*websocket.Conn, error) {
	parsed, err := url.Parse(endpoint)
	if token == "" || err != nil || (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("GUI requires an authenticated private WS endpoint")
	}
	dialer := websocket.Dialer{NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid GUI address")
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(addresses) == 0 {
			return nil, fmt.Errorf("GUI address unavailable")
		}
		for _, addr := range addresses {
			if !addr.IP.IsPrivate() && !addr.IP.IsLoopback() {
				return nil, fmt.Errorf("GUI endpoint must resolve only to private or loopback addresses")
			}
		}
		var connection net.Conn
		for _, addr := range addresses {
			connection, err = (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(addr.IP.String(), port))
			if err == nil {
				return connection, nil
			}
		}
		return nil, fmt.Errorf("GUI private endpoint unavailable")
	}}
	connection, response, err := dialer.DialContext(ctx, endpoint, http.Header{"Authorization": []string{"Bearer " + token}})
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("GUI private transport connection failed")
	}
	return connection, nil
}

func guiInstruction(args map[string]any) (string, error) {
	instruction, ok := args["instruction"].(string)
	if !ok || strings.TrimSpace(instruction) == "" || len([]rune(instruction)) > 32000 {
		return "", fmt.Errorf("GUI instruction must be nonempty text, at most 32000 characters")
	}
	return instruction, nil
}
