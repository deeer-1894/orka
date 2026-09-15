package db

import (
	"context"
	"go.mongodb.org/mongo-driver/bson"
	"time"
)

func (s *Storage) ClaimConversationExecution(ctx context.Context, owner, conversation, id string, ttl time.Duration) (bool, error) {
	now := time.Now()
	result, err := s.Conversations.UpdateOne(ctx, bson.M{"owner_email": owner, "conversation_id": conversation, "$or": []bson.M{
		{"execution_lease_id": bson.M{"$exists": false}},
		{"execution_lease_until": bson.M{"$lte": now.UnixMilli()}},
	}}, bson.M{"$set": bson.M{"execution_lease_id": id, "execution_lease_until": now.Add(ttl).UnixMilli()}})
	if err != nil {
		return false, err
	}
	return result.ModifiedCount == 1, nil
}
func (s *Storage) RenewConversationExecution(ctx context.Context, owner, conversation, id string, ttl time.Duration) (bool, error) {
	result, err := s.Conversations.UpdateOne(ctx, bson.M{"owner_email": owner, "conversation_id": conversation, "execution_lease_id": id}, bson.M{"$set": bson.M{"execution_lease_until": time.Now().Add(ttl).UnixMilli()}})
	if err != nil {
		return false, err
	}
	return result.MatchedCount == 1, nil
}
func (s *Storage) ReleaseConversationExecution(ctx context.Context, owner, conversation, id string) error {
	_, err := s.Conversations.UpdateOne(ctx, bson.M{"owner_email": owner, "conversation_id": conversation, "execution_lease_id": id}, bson.M{"$unset": bson.M{"execution_lease_id": "", "execution_lease_until": ""}})
	return err
}
