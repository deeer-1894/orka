package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/message_utils"
	"github.com/orka-oss/orka_core/messages"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestWorkflowPreparationCreatesConversationBeforeLease(t *testing.T) {
	uri := os.Getenv("ORKA_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set ORKA_TEST_MONGO_URI for isolated Mongo integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	database := client.Database("orka_workflow_prepare_test_" + messages.NewID())
	defer func() { database.Drop(context.Background()); client.Disconnect(context.Background()) }()
	store, err := db.NewStorage(ctx, uri, database.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(context.Background())
	s := &ChatService{Msg: &message_utils.Messenger{Store: store}}
	parent, release, run, err := s.prepareWorkflow(ctx, db.Workflow{OwnerEmail: "owner", WorkflowID: "wf", Steps: []db.WorkflowStep{{Name: "step"}}}, "conv")
	if err != nil {
		t.Fatalf("cannot admit new workflow conversation: %v", err)
	}
	defer release()
	if run.ConversationID != "conv" || run.RunID == "conv" || run.RunID != ExecutionID(parent) {
		t.Fatalf("identity=%+v", run)
	}
	saved, err := store.GetWorkflowRun(ctx, run.RunID, "owner")
	if err != nil || saved.Status != db.RunRunning {
		t.Fatalf("initial snapshot=%+v err=%v", saved, err)
	}
}
