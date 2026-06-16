// hermes-toolstream-proxy sits in front of the Hermes OpenAI-compatible API and
// makes its tool activity visible to a vanilla Open-WebUI frontend.
//
// Hermes runs its agent loop server-side and streams tool progress on a custom
// SSE channel — `event: hermes.tool.progress` — which OW (and any standard
// OpenAI client) discards, because OW only consumes `data:` chunks shaped like
// `choices[].delta`. This proxy rewrites those progress events into standard
// `delta.tool_calls` chunks, which OW renders natively as tool-call blocks.
// Everything else (the real completion chunks, non-stream responses, /health,
// /v1/models, …) is proxied verbatim.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	listen := getenv("TOOLSTREAM_LISTEN", ":8642")
	upstream := strings.TrimRight(getenv("TOOLSTREAM_UPSTREAM", "http://127.0.0.1:8643"), "/")
	log.Printf("hermes-toolstream-proxy: listening on %s, upstream %s", listen, upstream)
	// No client timeout: chat completions stream for as long as the agent runs.
	p := &proxy{upstream: upstream, client: &http.Client{}}
	log.Fatal(http.ListenAndServe(listen, p))
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

type proxy struct {
	upstream string
	client   *http.Client
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	out := p.upstream + r.URL.Path
	if r.URL.RawQuery != "" {
		out += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, out, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	copyHeaders(req.Header, r.Header)
	resp, err := p.client.Do(req)
	if err != nil {
		http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		translateSSE(w, resp.Body)
		return
	}
	io.Copy(w, resp.Body)
}

var hopByHop = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Transfer-Encoding": true,
	"Te": true, "Trailer": true, "Upgrade": true, "Proxy-Connection": true,
}

func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		if hopByHop[k] {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

type chunkMeta struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Created int64  `json:"created"`
}

type toolProgress struct {
	Tool       string `json:"tool"`
	Label      string `json:"label"`
	ToolCallID string `json:"toolCallId"`
	Status     string `json:"status"`
}

// translateSSE streams `body` to `w`, passing real completion chunks through
// unchanged and turning `hermes.tool.progress` events into content-embedded
// `<details type="tool_calls">` blocks — the exact markup Open-WebUI renders as
// native, collapsible tool-call cards.
//
// We deliberately do NOT emit `delta.tool_calls`: OW treats those as tools IT
// must execute and enters a tool-call retry loop, leaving the cards stuck on
// "Executing..." (the names aren't OW tools) and starving the final answer.
// Emitting the rendered `<details>` as plain `delta.content` (done="true")
// sidesteps that machinery entirely. Hermes never streams per-tool output on
// this endpoint, so the block carries only the command; the result lives in the
// final answer prose, which now flows through untouched. The card is emitted on
// the `completed` event (command captured from the matching `running`); any tool
// still pending at end-of-stream is flushed so it isn't lost.
func translateSSE(w io.Writer, body io.Reader) {
	flusher, _ := w.(http.Flusher)
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var eventName string
	var dataLines []string
	var meta chunkMeta
	running := map[string]toolProgress{}

	emitCard := func(ev toolProgress) {
		b, _ := json.Marshal(toolCardChunk(meta, ev))
		writeData(w, flusher, string(b))
	}

	flush := func() {
		event, data := eventName, strings.Join(dataLines, "\n")
		eventName, dataLines = "", dataLines[:0]

		switch event {
		case "":
			if strings.TrimSpace(data) != "[DONE]" {
				var m chunkMeta
				if json.Unmarshal([]byte(data), &m) == nil {
					if m.ID != "" {
						meta.ID = m.ID
					}
					if m.Model != "" {
						meta.Model = m.Model
					}
					if m.Created != 0 {
						meta.Created = m.Created
					}
				}
			}
			writeData(w, flusher, data)
		case "hermes.tool.progress":
			var ev toolProgress
			if json.Unmarshal([]byte(data), &ev) != nil {
				return
			}
			switch ev.Status {
			case "running":
				running[ev.ToolCallID] = ev // hold the command until completion
			case "completed":
				if started, ok := running[ev.ToolCallID]; ok {
					ev.Label = started.Label
					delete(running, ev.ToolCallID)
				}
				emitCard(ev)
			}
		default:
			// unknown custom event — OW can't use it, drop it.
		}
	}

	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(line[len("data:"):]))
		default:
			// id:, retry:, comments (":") — ignore.
		}
	}
	if len(dataLines) > 0 {
		flush()
	}
	for _, ev := range running { // tools still pending at stream end
		emitCard(ev)
	}
}

// toolCardChunk wraps a rendered `<details type="tool_calls">` card as a normal
// content delta so OW accumulates it into the assistant message and renders it
// as a completed tool-call card (done="true" → no spinner).
func toolCardChunk(meta chunkMeta, ev toolProgress) map[string]any {
	created := meta.Created
	if created == 0 {
		created = time.Now().Unix()
	}
	argKey := "label"
	switch ev.Tool {
	case "terminal":
		argKey = "command"
	case "skill_view":
		argKey = "skill"
	}
	args, _ := json.Marshal(map[string]string{argKey: ev.Label})
	// Hermes streams no per-tool output on this endpoint; the command lives in
	// `arguments`, the actual result in the final answer prose. Leave the result
	// body empty rather than echoing the command as if it were output.
	card := fmt.Sprintf(
		"\n<details type=\"tool_calls\" done=\"true\" id=%q name=%q arguments=%q>\n<summary>Tool Executed</summary>\n\"\"\n</details>\n",
		ev.ToolCallID, ev.Tool, html.EscapeString(string(args)),
	)
	return map[string]any{
		"id":      meta.ID,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   meta.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         map[string]any{"content": card},
			"finish_reason": nil,
		}},
	}
}

func writeData(w io.Writer, f http.Flusher, data string) {
	fmt.Fprintf(w, "data: %s\n\n", data)
	if f != nil {
		f.Flush()
	}
}
