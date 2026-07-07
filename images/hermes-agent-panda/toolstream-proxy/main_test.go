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
data: {"tool":"terminal","toolCallId":"toolu_02","status":"completed","result":"the answer is 4"}

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

	// We must NOT emit delta.tool_calls — OW would try to execute them.
	if strings.Contains(got, "tool_calls\":[{") || strings.Contains(got, `"function"`) {
		t.Fatalf("must not emit delta.tool_calls:\n%s", got)
	}

	// Exactly one tool card, emitted on completion as a content-embedded
	// done="true" <details> block carrying the command from the running event.
	var cards int
	for _, line := range strings.Split(got, "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || !strings.Contains(data, "details type=") {
			continue
		}
		cards++
		var chunk struct {
			ID, Model string
			Choices   []struct{ Delta struct{ Content string } }
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatalf("card chunk is not valid JSON: %v\n%s", err, data)
		}
		if chunk.ID != "chatcmpl-abc" || chunk.Model != "hermes-agent" {
			t.Errorf("card not attributed to source chunk: id=%q model=%q", chunk.ID, chunk.Model)
		}
		c := chunk.Choices[0].Delta.Content
		for _, want := range []string{`type="tool_calls"`, `done="true"`, `name="terminal"`, "Tool Executed", "panda execute", "the answer is 4"} {
			if !strings.Contains(c, want) {
				t.Errorf("card missing %q: %s", want, c)
			}
		}
	}
	if cards != 1 {
		t.Fatalf("expected 1 tool card (on completion), got %d", cards)
	}
}
