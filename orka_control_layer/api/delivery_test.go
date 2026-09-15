package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/service"
	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/delivery"
	"github.com/orka-oss/orka_core/pathsafe"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestDeliveryDownloadUsesFixedBytesAndConversationAuthorization(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, tc := range []struct {
		name, email, run, path string
		status                 int
	}{
		{"owner", "owner@example.test", "run-one", "report.txt", 200},
		{"viewer", "viewer@example.test", "run-one", "report.txt", 200},
		{"stranger", "stranger@example.test", "run-one", "report.txt", 404},
		{"different run", "owner@example.test", "run-other", "report.txt", 404},
		{"traversal", "owner@example.test", "run-one", "../report.txt", 404},
	} {
		mt.Run(tc.name, func(mt *mtest.T) {
			base := t.TempDir()
			root, err := pathsafe.EnsureSession(base, "owner@example.test", "one")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "report.txt"), []byte("published-v1"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := delivery.Publish(base, "owner@example.test", "one", "run-one", []string{"report.txt"}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "report.txt"), []byte("current-v2"), 0600); err != nil {
				t.Fatal(err)
			}
			mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, bson.D{
				{Key: "conversation_id", Value: "one"}, {Key: "owner_email", Value: "owner@example.test"},
				{Key: "shares", Value: bson.A{bson.D{{Key: "email", Value: "viewer@example.test"}, {Key: "role", Value: "viewer"}}}},
			}))
			a := &API{Store: &db.Storage{Conversations: mt.Coll}, BaseStorage: base}
			c := app.NewContext(0)
			c.Set("email", tc.email)
			c.Request.SetRequestURI("/delivery/download?conversation_id=one&run_id=" + tc.run + "&path=" + tc.path)
			a.DownloadDelivery(context.Background(), c)
			if c.Response.StatusCode() != tc.status {
				t.Fatalf("%d %s", c.Response.StatusCode(), c.Response.Body())
			}
			if tc.status == 200 && string(c.Response.Body()) != "published-v1" {
				t.Fatal("history download returned mutable workspace")
			}
		})
	}
}
func TestAcceptanceAuditRequiresRunOwnership(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("stranger", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, bson.D{{Key: "run_id", Value: "run-one"}, {Key: "owner_email", Value: "owner@example.test"}, {Key: "conversation_id", Value: "one"}}))
		a := &API{Store: &db.Storage{Runs: mt.Coll}, Chat: testAcceptanceService(t.TempDir())}
		c := app.NewContext(0)
		c.Set("email", "other@example.test")
		c.Request.SetBodyString(`{"run_id":"run-one"}`)
		a.GetAcceptance(context.Background(), c)
		if c.Response.StatusCode() != 404 {
			t.Fatalf("audit authorization=%d", c.Response.StatusCode())
		}
	})
}

func testAcceptanceService(base string) *service.ChatService {
	return &service.ChatService{Cfg: &config.Config{Storage: config.StorageConfig{BaseStoragePath: base}}}
}
