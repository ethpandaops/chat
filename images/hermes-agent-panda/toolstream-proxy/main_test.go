package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// A faithful slice of a real Hermes SSE stream: a role chunk, a tool-progress
// pair (running + completed), a content chunk, the stop chunk, and [DONE].
const hermesStream = `data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1781536710,"model":"hermes-agent","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}

event: hermes.tool.progress
data: {"tool":"terminal","emoji":"x","label":"panda execute --code 'print(1)'","toolCallId":"toolu_02","status":"running"}

event: hermes.tool.progress
data: {"tool":"terminal","toolCallId":"toolu_02","status":"completed"}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1781536710,"model":"hermes-agent","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":null}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1781536710,"model":"hermes-agent","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`

func TestTranslateSSE(t *testing.T) {
	var out strings.Builder
	translateSSE(&out, strings.NewReader(hermesStream))
	got := out.String()

	// The custom event must be gone; passthrough chunks must remain verbatim.
	if strings.Contains(got, "hermes.tool.progress") {
		t.Fatalf("custom event leaked into output:\n%s", got)
	}
	for _, want := range []string{`"role":"assistant"`, `"content":"done"`, `"finish_reason":"stop"`, "[DONE]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("passthrough lost %q:\n%s", want, got)
		}
	}

	// Exactly one synthesized tool_call (running only — completed is dropped),
	// in standard OpenAI shape, attributed to the real chunk id/model.
	var calls int
	for _, line := range strings.Split(got, "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || !strings.Contains(data, "tool_calls") {
			continue
		}
		calls++
		var chunk struct {
			ID, Model string
			Choices   []struct {
				Delta struct {
					ToolCalls []struct {
						Index    int
						ID, Type string
						Function struct{ Name, Arguments string }
					} `json:"tool_calls"`
				}
			}
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatalf("synthesized chunk is not valid JSON: %v\n%s", err, data)
		}
		tc := chunk.Choices[0].Delta.ToolCalls[0]
		if chunk.ID != "chatcmpl-abc" || chunk.Model != "hermes-agent" {
			t.Errorf("tool_call not attributed to source chunk: id=%q model=%q", chunk.ID, chunk.Model)
		}
		if tc.Type != "function" || tc.Function.Name != "terminal" || tc.ID != "toolu_02" || tc.Index != 0 {
			t.Errorf("unexpected tool_call: %+v", tc)
		}
		if !strings.Contains(tc.Function.Arguments, "panda execute") {
			t.Errorf("command not carried into arguments: %q", tc.Function.Arguments)
		}
	}
	if calls != 1 {
		t.Fatalf("expected 1 synthesized tool_call (completed dropped), got %d", calls)
	}
}
