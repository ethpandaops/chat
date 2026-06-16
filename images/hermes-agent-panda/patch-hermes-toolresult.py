#!/usr/bin/env python3
"""Build-time patch: stream the tool RESULT on Hermes' chat-completions SSE.

Hermes' OpenAI-compatible endpoint emits `hermes.tool.progress` events but
deliberately omits the tool output ("No tool output travels here — output is the
agent's concern ... not streamed as presentation", gateway/stream_events.py). The
result is nonetheless in scope at `_on_tool_complete(... function_result)`; we add
it to the `completed` event so the toolstream proxy can surface it as the tool
card's OUTPUT in Open-WebUI. Capped to keep the SSE small.

Fail loud if the upstream block moves (same guard philosophy as open-webui-cf's
patch.py) so a Hermes bump can't silently no-op this.
"""
import pathlib

FILE = pathlib.Path("/opt/hermes/gateway/platforms/api_server.py")

TARGET = (
    '                _stream_q.put(("__tool_progress__", {\n'
    '                    "tool": function_name,\n'
    '                    "toolCallId": tool_call_id,\n'
    '                    "status": "completed",\n'
    '                }))'
)
INJECT = (
    '                _stream_q.put(("__tool_progress__", {\n'
    '                    "tool": function_name,\n'
    '                    "toolCallId": tool_call_id,\n'
    '                    "status": "completed",\n'
    '                    "result": (function_result if isinstance(function_result, str) else str(function_result))[:8000],\n'
    '                }))'
)

src = FILE.read_text()
n = src.count(TARGET)
assert n == 1, f"expected exactly 1 _on_tool_complete completed-event block, found {n} — Hermes layout changed"
FILE.write_text(src.replace(TARGET, INJECT))
print("patched: chat-completions tool.completed event now carries 'result'")
