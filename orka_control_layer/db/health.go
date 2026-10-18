package db

import (
	"context"
	"errors"
)

// Ping checks the existing connection without opening a model/tool execution.
func (s *Storage) Ping(ctx context.Context) error {
	if s == nil || s.client == nil {
		return errors.New("mongo connection unavailable")
	}
	return s.client.Ping(ctx, nil)
}
