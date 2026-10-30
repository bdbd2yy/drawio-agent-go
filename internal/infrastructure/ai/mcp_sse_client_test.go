package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestMCPBoundedResultAndFailure(t *testing.T) {
	for _, kind := range []string{"valid", "size", "failure", "timeout"} {
		t.Run(kind, func(t *testing.T) {
			var mu sync.Mutex
			var stream http.ResponseWriter
			ready := make(chan struct{})
			closed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "event: endpoint\ndata: /rpc\n\n")
					w.(http.Flusher).Flush()
					mu.Lock()
					stream = w
					mu.Unlock()
					close(ready)
					<-r.Context().Done()
					close(closed)
					return
				}
				var req struct {
					ID     uint64 `json:"id"`
					Method string `json:"method"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				w.WriteHeader(202)
				if req.ID == 0 {
					return
				}
				<-ready
				result := `{}`
				if req.Method == "tools/call" {
					switch kind {
					case "valid":
						result = `{"content":[{"type":"text","text":"ok"}]}`
					case "size":
						result = `{"content":[{"type":"text","text":"big"}]}`
					case "failure":
						result = `{"isError":true,"content":[{"type":"text","text":"failed"}]}`
					case "timeout":
						return
					}
				}
				mu.Lock()
				fmt.Fprintf(stream, "event: message\ndata: {\"id\":%d,\"result\":%s}\n\n", req.ID, result)
				stream.(http.Flusher).Flush()
				mu.Unlock()
			}))
			defer server.Close()
			client := NewMCPSSEClient(server.URL, "/sse", 1000)
			client.maxResultBytes = 2
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			out, err := client.CallTool(ctx, "t", `{}`)
			client.shutdown()
			if kind == "valid" {
				if err != nil || out != "ok" {
					t.Fatalf("%q %v", out, err)
				}
			} else if err == nil {
				t.Fatal("invalid tool result accepted")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("SSE reader not canceled")
			}
			client.mu.Lock()
			pending := len(client.pending)
			client.mu.Unlock()
			if pending != 0 {
				t.Fatal("pending RPC leaked")
			}
		})
	}
}
