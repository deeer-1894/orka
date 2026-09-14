package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/orka-oss/orka_control_layer/db"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionDownloadAuthorization(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, tc := range []struct {
		name, email, query string
		status             int
	}{
		{"owner", "owner@example.com", "conv=one&path=report.txt", 200},
		{"viewer", "viewer@example.com", "conv=one&path=report.txt", 200},
		{"stranger", "stranger@example.com", "conv=one&path=report.txt", 404},
		{"missing", "owner@example.com", "path=report.txt", 400},
		{"traversal", "owner@example.com", "conv=one&path=../two/report.txt", 400},
	} {
		mt.Run(tc.name, func(mt *mtest.T) {
			base := t.TempDir()
			for _, dir := range []string{"one", "two"} {
				p := filepath.Join(base, "owner@example.com", "sessions", dir)
				if err := os.MkdirAll(p, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(p, "report.txt"), []byte(dir), 0600); err != nil {
					t.Fatal(err)
				}
			}
			mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, bson.D{
				{Key: "conversation_id", Value: "one"}, {Key: "owner_email", Value: "owner@example.com"},
				{Key: "shares", Value: bson.A{bson.D{{Key: "email", Value: "viewer@example.com"}, {Key: "role", Value: "viewer"}}}},
			}))
			a := &API{Store: &db.Storage{Conversations: mt.Coll}, BaseStorage: base}
			c := app.NewContext(0)
			c.Set("email", tc.email)
			c.Request.SetRequestURI("/file/download?" + tc.query)
			a.FileDownload(context.Background(), c)
			if c.Response.StatusCode() != tc.status {
				t.Fatalf("status=%d body=%s", c.Response.StatusCode(), c.Response.Body())
			}
			if tc.status == 200 && string(c.Response.Body()) != "one" {
				t.Fatalf("wrong session: %s", c.Response.Body())
			}
		})
	}
}

func TestSessionFileOperations(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, tc := range []struct {
		name, email, uri, body, handler string
		status                          int
	}{
		{"chat viewer", "viewer@example.com", "/chat/run", `{"conversation_id":"one","message":"write"}`, "chat", 403},
		{"chat stranger", "stranger@example.com", "/chat/run", `{"conversation_id":"one","message":"write"}`, "chat", 404},
		{"list owner", "owner@example.com", "/file/list", `{"conversation_id":"one","path":"."}`, "list", 200},
		{"list viewer", "viewer@example.com", "/file/list", `{"conversation_id":"one"}`, "list", 200},
		{"delete viewer", "viewer@example.com", "/file/delete", `{"conversation_id":"one","path":"report.txt"}`, "delete", 403},
		{"delete editor", "editor@example.com", "/file/delete", `{"conversation_id":"one","path":"report.txt"}`, "delete", 200},
		{"delete root", "owner@example.com", "/file/delete", `{"conversation_id":"one","path":"."}`, "delete", 400},
		{"conflict", "owner@example.com", "/file/list?conv=two", `{"conversation_id":"one"}`, "list", 400},
		{"unknown", "owner@example.com", "/file/list", `{"conversation_id":"unknown"}`, "list", 404},
		{"unauthenticated", "", "/file/list", `{"conversation_id":"one"}`, "list", 401},
		{"symlink", "owner@example.com", "/file/download?conv=one&path=escape/report.txt", "", "download", 404},
		{"file url", "owner@example.com", "/file/get-file-url", `{"conversation_id":"one","path":"a &中.txt"}`, "url", 200},
	} {
		mt.Run(tc.name, func(mt *mtest.T) {
			base := t.TempDir()
			root := filepath.Join(base, "owner@example.com", "sessions", "one")
			os.MkdirAll(root, 0775)
			os.WriteFile(filepath.Join(root, "report.txt"), []byte("one"), 0664)
			os.WriteFile(filepath.Join(root, "a &中.txt"), []byte("special"), 0664)
			outside := t.TempDir()
			os.WriteFile(filepath.Join(outside, "report.txt"), []byte("secret"), 0644)
			os.Symlink(outside, filepath.Join(root, "escape"))
			doc := bson.D{{Key: "conversation_id", Value: "one"}, {Key: "owner_email", Value: "owner@example.com"}, {Key: "shares", Value: bson.A{
				bson.D{{Key: "email", Value: "viewer@example.com"}, {Key: "role", Value: "viewer"}}, bson.D{{Key: "email", Value: "editor@example.com"}, {Key: "role", Value: "editor"}},
			}}}
			if tc.name == "unknown" {
				mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch))
			} else {
				mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, doc))
			}
			a := &API{Store: &db.Storage{Conversations: mt.Coll}, BaseStorage: base, chunks: newChunkManager()}
			c := app.NewContext(0)
			if tc.email != "" {
				c.Set("email", tc.email)
			}
			c.Request.SetRequestURI(tc.uri)
			c.Request.SetBodyString(tc.body)
			handlers := map[string]func(context.Context, *app.RequestContext){"list": a.FileList, "delete": a.FileDelete, "download": a.FileDownload, "url": a.GetFileURL, "chat": a.ChatRun}
			handlers[tc.handler](context.Background(), c)
			if c.Response.StatusCode() != tc.status {
				t.Fatalf("status=%d body=%s", c.Response.StatusCode(), c.Response.Body())
			}
			if tc.name == "file url" {
				var got struct {
					Data map[string]string `json:"data"`
				}
				json.Unmarshal(c.Response.Body(), &got)
				u, err := url.Parse(got.Data["url"])
				if err != nil || u.Query().Get("conv") != "one" || u.Query().Get("path") != "a &中.txt" {
					t.Fatalf("bad scoped URL: %s", c.Response.Body())
				}
			}
			if tc.name == "delete viewer" {
				if _, err := os.Stat(filepath.Join(root, "report.txt")); err != nil {
					t.Fatal("viewer deleted file")
				}
			}
		})
	}
}

func TestSessionChunkUploadsAreBoundToUserAndConversation(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("chunks", func(mt *mtest.T) {
		base := t.TempDir()
		a := &API{Store: &db.Storage{Conversations: mt.Coll}, BaseStorage: base, chunks: newChunkManager()}
		call := func(user, conv string, index, total int, data string, progress bool) *app.RequestContext {
			mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, bson.D{{Key: "conversation_id", Value: conv}, {Key: "owner_email", Value: user}}))
			c := app.NewContext(0)
			c.Set("email", user)
			if progress {
				c.Request.SetRequestURI("/file/upload-progress?conv=" + conv + "&upload_id=same")
				a.FileUploadProgress(context.Background(), c)
			} else {
				b, _ := json.Marshal(map[string]any{"conversation_id": conv, "upload_id": "same", "filename": "report.txt", "index": index, "total": total, "data": base64.StdEncoding.EncodeToString([]byte(data))})
				c.Request.SetBody(b)
				a.FileUploadChunk(context.Background(), c)
			}
			return c
		}
		if c := call("owner", "one", 0, 2, "first-", false); c.Response.StatusCode() != 200 {
			t.Fatal(string(c.Response.Body()))
		}
		for _, pair := range [][2]string{{"owner", "two"}, {"other", "one"}} {
			if c := call(pair[0], pair[1], 0, 0, "", true); c.Response.StatusCode() != 404 {
				t.Fatal("upload progress crossed scope")
			}
		}
		if c := call("owner", "one", 1, 3, "bad", false); c.Response.StatusCode() != 400 {
			t.Fatal("metadata change accepted")
		}
		if c := call("owner", "one", 1, 2, "last", false); c.Response.StatusCode() != 200 || !strings.Contains(string(c.Response.Body()), `"complete":true`) {
			t.Fatal(string(c.Response.Body()))
		}
		data, err := os.ReadFile(filepath.Join(base, "owner", "sessions", "one", "report.txt"))
		if err != nil || string(data) != "first-last" {
			t.Fatalf("assembled=%s %v", data, err)
		}
	})
}
