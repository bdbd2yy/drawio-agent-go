package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ai-agent-scaffold-go/internal/domain/agent/ports"
)

type OpenAIClient struct {
	httpClient     *http.Client
	completionsURL string
	apiKey         string
	model          string
}

type OpenAIToolDef struct {
	Name        string
	Description string
}

func NewOpenAIClient(completionsURL, apiKey, model string, requestTimeout time.Duration) *OpenAIClient {
	if requestTimeout <= 0 {
		requestTimeout = 5 * time.Minute
	}
	return &OpenAIClient{
		httpClient:     &http.Client{Timeout: requestTimeout},
		completionsURL: completionsURL,
		apiKey:         apiKey,
		model:          model,
	}
}

func (c *OpenAIClient) Generate(ctx context.Context, messages []ports.ChatMessage, tools []OpenAIToolDef) (ports.ChatReply, error) {
	body, err := buildRequestBody(c.model, messages, tools, false)
	if err != nil {
		return ports.ChatReply{}, err
	}
	resp, err := c.do(ctx, body)
	if err != nil {
		return ports.ChatReply{}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return ports.ChatReply{}, fmt.Errorf("openai read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ports.ChatReply{}, fmt.Errorf("openai upstream %d: %s", resp.StatusCode, truncate(string(raw), 400))
	}

	var parsed openaiCompletion
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ports.ChatReply{}, fmt.Errorf("openai decode: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return ports.ChatReply{}, fmt.Errorf("openai response has no choices")
	}
	choice := parsed.Choices[0].Message
	reply := ports.ChatReply{Content: choice.Content}
	for _, tc := range choice.ToolCalls {
		reply.ToolCalls = append(reply.ToolCalls, ports.ChatToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return reply, nil
}

func (c *OpenAIClient) Stream(ctx context.Context, messages []ports.ChatMessage, tools []OpenAIToolDef) (<-chan ports.ChatStreamEvent, <-chan error) {
	events := make(chan ports.ChatStreamEvent, 8)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)

		body, err := buildRequestBody(c.model, messages, tools, true)
		if err != nil {
			errs <- err
			return
		}
		resp, err := c.do(ctx, body)
		if err != nil {
			errs <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			raw, _ := io.ReadAll(resp.Body)
			errs <- fmt.Errorf("openai upstream %d: %s", resp.StatusCode, truncate(string(raw), 400))
			return
		}

		toolCallBuf := map[int]*ports.ChatToolCall{}
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					emitToolCalls(events, toolCallBuf)
					events <- ports.ChatStreamEvent{Done: true}
					return
				}
				errs <- fmt.Errorf("openai stream read: %w", err)
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" || !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "[DONE]" {
				emitToolCalls(events, toolCallBuf)
				events <- ports.ChatStreamEvent{Done: true}
				return
			}
			var chunk openaiStreamChunk
			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				errs <- fmt.Errorf("openai stream decode: %w", err)
				return
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			delta := chunk.Choices[0].Delta
			if delta.Content != "" {
				select {
				case events <- ports.ChatStreamEvent{Delta: delta.Content}:
				case <-ctx.Done():
					errs <- ctx.Err()
					return
				}
			}
			for _, tc := range delta.ToolCalls {
				current, ok := toolCallBuf[tc.Index]
				if !ok {
					current = &ports.ChatToolCall{}
					toolCallBuf[tc.Index] = current
				}
				if tc.ID != "" {
					current.ID = tc.ID
				}
				if tc.Function.Name != "" {
					current.Name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					current.Arguments += tc.Function.Arguments
				}
			}
		}
	}()
	return events, errs
}

func emitToolCalls(events chan<- ports.ChatStreamEvent, buf map[int]*ports.ChatToolCall) {
	if len(buf) == 0 {
		return
	}
	calls := make([]ports.ChatToolCall, 0, len(buf))
	for i := 0; i < len(buf); i++ {
		if call, ok := buf[i]; ok && call != nil {
			calls = append(calls, *call)
		}
	}
	if len(calls) > 0 {
		events <- ports.ChatStreamEvent{ToolCalls: calls}
	}
}

func (c *OpenAIClient) do(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.completionsURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai http: %w", err)
	}
	return resp, nil
}

func buildRequestBody(modelName string, messages []ports.ChatMessage, tools []OpenAIToolDef, stream bool) ([]byte, error) {
	payload := map[string]any{
		"model":    modelName,
		"messages": encodeMessages(messages),
		"stream":   stream,
	}
	if len(tools) > 0 {
		payload["tools"] = encodeTools(tools)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("openai marshal request: %w", err)
	}
	return raw, nil
}

func encodeMessages(messages []ports.ChatMessage) []map[string]any {
	encoded := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		entry := map[string]any{"role": string(m.Role)}
		if m.Content != "" {
			entry["content"] = m.Content
		} else if m.Role != ports.ChatRoleAssistant || len(m.ToolCalls) == 0 {
			entry["content"] = ""
		}
		if m.Name != "" {
			entry["name"] = m.Name
		}
		if m.ToolCallID != "" {
			entry["tool_call_id"] = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(m.ToolCalls))
			for _, c := range m.ToolCalls {
				calls = append(calls, map[string]any{
					"id":   c.ID,
					"type": "function",
					"function": map[string]any{
						"name":      c.Name,
						"arguments": c.Arguments,
					},
				})
			}
			entry["tool_calls"] = calls
		}
		encoded = append(encoded, entry)
	}
	return encoded
}

func encodeTools(tools []OpenAIToolDef) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": fallbackDescription(t),
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "自由文本输入或检索查询，工具会按其语义解释",
						},
					},
				},
			},
		})
	}
	return out
}

func fallbackDescription(t OpenAIToolDef) string {
	if strings.TrimSpace(t.Description) != "" {
		return t.Description
	}
	return "外部工具 " + t.Name + "，参数 query 为自由文本"
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

type openaiCompletion struct {
	Choices []struct {
		Message struct {
			Content   string             `json:"content"`
			ToolCalls []openaiToolCallV1 `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}

type openaiStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string                 `json:"content"`
			ToolCalls []openaiStreamToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

type openaiToolCallV1 struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openaiStreamToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}
