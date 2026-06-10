package llm

import (
	"context"
	"sync"
)

// MockClient returns scripted responses in order. Useful for tests and for the
// control layer's default offline behavior.
type MockClient struct {
	mu          sync.Mutex
	Responses   []Response
	i           int
	Requests    []Request // captured for assertions
	FallbackMsg string    // returned once Responses are exhausted
}

// NewMock builds a MockClient from the given scripted responses.
func NewMock(resps ...Response) *MockClient {
	return &MockClient{Responses: resps, FallbackMsg: "done"}
}

// Chat implements Client.
func (m *MockClient) Chat(_ context.Context, req Request) (Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Requests = append(m.Requests, req)
	if m.i < len(m.Responses) {
		r := m.Responses[m.i]
		m.i++
		return r, nil
	}
	return Response{Content: m.FallbackMsg, FinishReason: "stop"}, nil
}

// Calls reports how many times Chat was invoked.
func (m *MockClient) Calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Requests)
}
