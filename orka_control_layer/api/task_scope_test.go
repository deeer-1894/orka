package api

import (
	"context"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/orka-oss/orka_control_layer/db"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestChatRunRejectsTaskOutsideConversation(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, tc := range []struct{ name, owner, conv string }{{"foreign owner", "victim", "other"}, {"same owner other conversation", "attacker", "other"}} {
		mt.Run(tc.name, func(mt *mtest.T) {
			mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, bson.D{{Key: "conversation_id", Value: "mine"}, {Key: "owner_email", Value: "attacker"}}), mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, bson.D{{Key: "task_id", Value: "target"}, {Key: "conversation_id", Value: tc.conv}, {Key: "owner_email", Value: tc.owner}}))
			a := &API{Store: &db.Storage{Conversations: mt.Coll, Tasks: mt.Coll}, BaseStorage: t.TempDir()}
			c := app.NewContext(0)
			c.Set("email", "attacker")
			c.Request.SetBodyString(`{"conversation_id":"mine","task_id":"target","message":"hello"}`)
			// No chat executor is configured: rejecting the untrusted association must
			// happen before any execution or side effects are attempted.
			a.ChatRun(context.Background(), c)
			if c.Response.StatusCode() != 404 {
				t.Fatalf("task association accepted: %d %s", c.Response.StatusCode(), c.Response.Body())
			}
		})
	}
}
