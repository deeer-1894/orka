package llm

import (
	"context"
	"io"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type streamingOptionClient struct{ *MockClient }

func (c streamingOptionClient) ChatStream(ctx context.Context, req Request, emit func(string)) (Response, error) {
	return c.Chat(ctx, req)
}

func TestEinoCallOptionsReachClient(t *testing.T) {
	for _, mode := range []string{"generate", "stream", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			mock := NewMock(Response{Content: "ok"})
			var client Client = mock
			if mode == "stream" {
				client = streamingOptionClient{mock}
			}
			m := NewEinoModel(client, "default")
			opts := []model.Option{model.WithMaxTokens(1024), model.WithMaxTokens(512), model.WithModel("override"), model.WithTemperature(0.25)}
			input := []*schema.Message{schema.UserMessage("hi")}
			if mode == "generate" {
				if _, err := m.Generate(context.Background(), input, opts...); err != nil {
					t.Fatal(err)
				}
			} else {
				stream, err := m.Stream(context.Background(), input, opts...)
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				for {
					_, err = stream.Recv()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			req := mock.Requests[0]
			if req.MaxTokens != 512 || req.Model != "override" || req.Temperature != .25 {
				t.Fatalf("options lost: %+v", req)
			}
			if _, err := m.Generate(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			req = mock.Requests[1]
			if req.Model != "default" || req.MaxTokens != 0 || req.Temperature != 0 {
				t.Fatalf("call options leaked: %+v", req)
			}
		})
	}
}
