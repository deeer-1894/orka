package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// OAuthPending is an authorization the user started and the provider has not
// called back for yet. Stored rather than held in memory so whichever instance
// receives the callback can finish it; single-use and short-lived. Secrets in
// it are sealed by the caller.
type OAuthPending struct {
	State        string    `bson:"state"`
	Owner        string    `bson:"owner"`
	Provider     string    `bson:"provider"`
	Name         string    `bson:"name"`
	MCPURL       string    `bson:"mcp_url"`
	ConnectorID  string    `bson:"connector_id,omitempty"`
	Redirect     string    `bson:"redirect"`
	Verifier     string    `bson:"verifier"`
	ClientID     string    `bson:"client_id"`
	ClientSecret string    `bson:"client_secret,omitempty"`
	TokenURL     string    `bson:"token_url"`
	Resource     string    `bson:"resource,omitempty"`
	Scopes       string    `bson:"scopes,omitempty"`
	JSONToken    bool      `bson:"json_token,omitempty"`
	ExpiresAt    time.Time `bson:"expires_at"` // TTL index removes it afterwards
}

func (s *Storage) SavePendingOAuth(ctx context.Context, p *OAuthPending) error {
	if _, err := s.OAuthPending.InsertOne(ctx, p); err != nil {
		return fmt.Errorf("save pending oauth: %w", err)
	}
	return nil
}

// TakePendingOAuth claims a pending authorization exactly once.
func (s *Storage) TakePendingOAuth(ctx context.Context, state string) (*OAuthPending, error) {
	var out OAuthPending
	err := s.OAuthPending.FindOneAndDelete(ctx, bson.M{"state": state, "expires_at": bson.M{"$gt": time.Now()}}).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("take pending oauth: %w", err)
	}
	return &out, nil
}

// CountPendingOAuth bounds how many authorizations one user can have open.
func (s *Storage) CountPendingOAuth(ctx context.Context, owner string) (int64, error) {
	return s.OAuthPending.CountDocuments(ctx, bson.M{"owner": owner, "expires_at": bson.M{"$gt": time.Now()}})
}

// GetConnector returns one of the owner's connectors.
func (s *Storage) GetConnector(ctx context.Context, id, owner string) (*Connector, error) {
	var out Connector
	err := s.Connectors.FindOne(ctx, bson.M{"connector_id": id, "owner_email": owner}).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get connector: %w", err)
	}
	return &out, nil
}

// SaveConnectorOAuthIf replaces a grant only if its stored refresh token is
// still expectRefresh: the instance that loses a concurrent refresh learns so
// and reloads, instead of overwriting the winner's rotated tokens.
func (s *Storage) SaveConnectorOAuthIf(ctx context.Context, id, owner, expectRefresh string, grant *ConnectorOAuth) (bool, error) {
	res, err := s.Connectors.UpdateOne(ctx,
		bson.M{"connector_id": id, "owner_email": owner, "oauth.refresh_token": expectRefresh},
		bson.M{"$set": bson.M{"oauth": grant}})
	if err != nil {
		return false, fmt.Errorf("save connector grant: %w", err)
	}
	return res.MatchedCount == 1, nil
}

var pendingOAuthIndex = mongo.IndexModel{
	Keys:    bson.D{{Key: "expires_at", Value: 1}},
	Options: options.Index().SetExpireAfterSeconds(0).SetName("oauth_pending_ttl"),
}
