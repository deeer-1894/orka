package api

import (
	"context"
	"testing"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/message_utils"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestFollowupsRequiresAuthenticatedRunIdentity(t *testing.T) {
	for _, tc := range []struct {
		owner, body string
		status      int
	}{
		{"", `{"conversation_id":"c","run_id":"r","model_profile":"revision"}`, 401},
		{"alice", `{"prompt":"injected","answer":"injected","model_profile":"revision"}`, 400},
		{"alice", `{"conversation_id":"c","model_profile":"revision"}`, 400},
		{"alice", `{"run_id":"r","model_profile":"revision"}`, 400},
	} {
		a := settingsAPI(t)
		c := settingsRequest(tc.owner, tc.body)
		a.Followups(context.Background(), c)
		if c.Response.StatusCode() != tc.status {
			t.Errorf("got %d want %d: %s", c.Response.StatusCode(), tc.status, c.Response.Body())
		}
	}
}

func TestFollowupsRejectsForeignOrMismatchedRun(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, tc := range []struct{ name, owner, cid string }{
		{"foreign owner", "bob", "c"}, {"wrong conversation", "alice", "other"},
	} {
		mt.Run(tc.name, func(mt *mtest.T) {
			a := settingsAPI(mt.T)
			a.Chat.Msg = &message_utils.Messenger{Store: &db.Storage{Runs: mt.Coll}}
			mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, bson.D{
				{Key: "run_id", Value: "r"}, {Key: "owner_email", Value: tc.owner}, {Key: "conversation_id", Value: tc.cid},
				{Key: "status", Value: db.RunDone}, {Key: "prompt", Value: "private"}, {Key: "output", Value: "private"}, {Key: "model", Value: "deployment"},
			}))
			c := settingsRequest("alice", `{"conversation_id":"c","run_id":"r","model_profile":"revision"}`)
			a.Followups(context.Background(), c)
			if c.Response.StatusCode() != 404 {
				mt.Fatalf("got %d want 404: %s", c.Response.StatusCode(), c.Response.Body())
			}
		})
	}
}
