package service

import (
	"context"
	"github.com/orka-oss/orka_control_layer/db"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
	"testing"
)

func suggestRecordedFollowups(t *testing.T, svc *ChatService, model, profile, prompt, answer string) (out []string, err error) {
	t.Helper()
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("persisted followup", func(mt *mtest.T) {
		previous := svc.Msg.Store
		svc.Msg.Store = &db.Storage{Runs: mt.Coll}
		defer func() { svc.Msg.Store = previous }()
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, bson.D{
			{Key: "run_id", Value: "recorded"}, {Key: "owner_email", Value: "owner"},
			{Key: "conversation_id", Value: "conv"}, {Key: "status", Value: db.RunDone},
			{Key: "prompt", Value: prompt}, {Key: "output", Value: answer}, {Key: "model", Value: model},
		}))
		out, err = svc.SuggestFollowupsForRun(context.Background(), "owner", "conv", "recorded", profile)
	})
	return out, err
}
