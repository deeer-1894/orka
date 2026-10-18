package tools

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

type responseTransport func(*http.Request) (*http.Response, error)

func (f responseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failedResponseBody struct{}

func (failedResponseBody) Read([]byte) (int, error) { return 0, errors.New("response interrupted") }
func (failedResponseBody) Close() error             { return nil }

func TestHTTPRequestCompleteJSONAndReadFailure(t *testing.T) {
	largeJSON := `{"body":"` + strings.Repeat("x", 96<<10) + `"}`
	for _, tt := range []struct {
		name      string
		body      io.ReadCloser
		wantError bool
		want      string
	}{
		{"complete large JSON", io.NopCloser(strings.NewReader(largeJSON)), false, largeJSON},
		{"read failure", failedResponseBody{}, true, "response interrupted"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Status: "200 OK", Body: tt.body, Header: make(http.Header), Request: r}, nil
			})}
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]any{"url": "https://203.0.113.10/releases"}
			result, err := httpRequestWithClient(client)(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError != tt.wantError {
				t.Fatalf("IsError = %v, want %v", result.IsError, tt.wantError)
			}
			text := result.Content[0].(mcp.TextContent).Text
			if !strings.Contains(text, tt.want) {
				t.Fatalf("response incomplete: got %d bytes, expected content of %d bytes", len(text), len(tt.want))
			}
		})
	}
}

func TestHTTPBodyBoundary(t *testing.T) {
	for _, size := range []int{maxHTTPBodyBytes - 1, maxHTTPBodyBytes, maxHTTPBodyBytes + 1} {
		body, err := readHTTPBody(strings.NewReader(strings.Repeat("x", size)))
		if size <= maxHTTPBodyBytes {
			if err != nil || len(body) != size {
				t.Fatalf("size=%d: bytes=%d, err=%v", size, len(body), err)
			}
		} else if err == nil || len(body) != 0 || !strings.Contains(err.Error(), "smaller page") {
			t.Fatalf("oversized response exposed a parseable prefix: bytes=%d, err=%v", len(body), err)
		}
	}
}

func TestHTTPErrorStatusAndGuard(t *testing.T) {
	called := false
	client := &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: 429, Status: "429 Too Many Requests", Header: make(http.Header), Request: r, Body: io.NopCloser(strings.NewReader(`{"message":"rate limited"}`))}, nil
	})}
	handler := httpRequestWithClient(client)
	for _, address := range []string{"http://127.0.0.1/private", "https://203.0.113.10/releases"} {
		called = false
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]any{"url": address}
		result, err := handler(context.Background(), req)
		if err != nil || !result.IsError {
			t.Fatalf("address %s: %v %v", address, result, err)
		}
		if strings.Contains(address, "127.0.0.1") {
			if called {
				t.Fatal("private target reached transport")
			}
		} else if !strings.Contains(result.Content[0].(mcp.TextContent).Text, `"rate limited"`) {
			t.Fatal("HTTP failure lost provider response")
		}
	}
}
