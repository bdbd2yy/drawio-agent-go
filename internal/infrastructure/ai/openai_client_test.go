package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ai-agent-scaffold-go/internal/domain/agent/ports"
	execution "ai-agent-scaffold-go/internal/domain/agent/runtime"
)

func TestOpenAIResponseBounds(t *testing.T) {
	for _, size := range []int{15, 16, 17} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, strings.Repeat("a", size))
			}))
			defer server.Close()
			o := execution.Defaults()
			o.MaxModelBytes = 16
			ctx, cancel := execution.NewContext(context.Background(), o)
			defer cancel()
			_, err := NewOpenAIClient(server.URL, "", "test", time.Second).Generate(ctx, nil, nil)
			if size > 16 {
				if !errors.Is(err, execution.ErrSize) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestOpenAICancel(t *testing.T) {
	entered := make(chan struct{})
	exited := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(exited)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := NewOpenAIClient(server.URL, "", "test", time.Second).Generate(ctx, nil, nil)
		done <- err
	}()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("upstream request not canceled")
	}
}
func TestOpenAIStream(t *testing.T) {
	for _, kind := range []string{"valid", "truncated", "size", "invalid", "tool"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if kind == "invalid" {
					fmt.Fprint(w, "data: not-json\n\n")
					return
				}
				if kind == "tool" {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"i\",\"function\":{\"name\":\"t\",\"arguments\":\"12345\"}}]}}]}\n\n")
				} else {
					v := "ok"
					if kind == "size" {
						v = "12345"
					}
					fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", v)
				}
				if kind != "truncated" {
					fmt.Fprint(w, "data: [DONE]\n\n")
				}
			}))
			defer server.Close()
			o := execution.Defaults()
			o.MaxModelBytes = 4
			ctx, cancel := execution.NewContext(context.Background(), o)
			defer cancel()
			events, errs := NewOpenAIClient(server.URL, "", "test", time.Second).Stream(ctx, nil, nil)
			var done bool
			for event := range events {
				done = done || event.Done
			}
			var err error
			for e := range errs {
				err = e
			}
			if kind == "valid" {
				if err != nil || !done {
					t.Fatalf("%v done=%v", err, done)
				}
			} else if err == nil || done {
				t.Fatalf("invalid stream accepted: %v done=%v", err, done)
			}
		})
	}
}
func TestOpenAIUnreadStreamCancels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 32; i++ {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	_, errs := NewOpenAIClient(server.URL, "", "test", time.Second).Stream(ctx, []ports.ChatMessage{}, nil)
	cancel()
	select {
	case <-errs:
	case <-time.After(time.Second):
		t.Fatal("blocked sender did not exit")
	}
}
