package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/orka-oss/orka_control_layer/db"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestLegacyWorkspaceVersionCanRestore(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, stamp := range []string{"20260915-120000", "20260915-120000.123456789"} {
		mt.Run(stamp, func(mt *mtest.T) {
			base := t.TempDir()
			root := filepath.Join(base, "fixture@example.test", "sessions", "one")
			p := filepath.Join(root, trashDir, stamp, "note.txt")
			if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(p, []byte("previous"), 0600)
			os.WriteFile(filepath.Join(root, "note.txt"), []byte("current"), 0600)
			mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, bson.D{{Key: "conversation_id", Value: "one"}, {Key: "owner_email", Value: "fixture@example.test"}}))
			a := &API{Store: &db.Storage{Conversations: mt.Coll}, BaseStorage: base}
			c := app.NewContext(0)
			c.Set("email", "fixture@example.test")
			c.Request.Header.Set("Content-Type", "application/json")
			c.Request.SetBodyString(fmt.Sprintf(`{"conversation_id":"one","path":"note.txt","ts":%q}`, stamp))
			a.FileRestore(context.Background(), c)
			if c.Response.StatusCode() != 200 {
				t.Fatalf("restore rejected: %d %s", c.Response.StatusCode(), c.Response.Body())
			}
			b, _ := os.ReadFile(filepath.Join(root, "note.txt"))
			if string(b) != "previous" {
				t.Fatalf("restore=%q", b)
			}
			if parseStampMillis(stamp) == 0 {
				t.Fatal("legacy date not parsed")
			}
		})
	}
}
