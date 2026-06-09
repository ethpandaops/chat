"""
Patch open-webui to forward Cf-Access-Jwt-Assertion from the incoming
browser request to the upstream OpenAI-compatible model endpoint.

Target: backend/open_webui/routers/openai.py — get_headers_and_cookies()
The function already builds the outbound headers dict; we insert three
lines before its return so the CF Access JWT rides along to Hermes.
"""

FILE = "/app/backend/open_webui/routers/openai.py"

with open(FILE) as f:
    src = f.read()

TARGET = "    return headers, cookies\n"
INSERTION = (
    '    cf_jwt = request.headers.get("Cf-Access-Jwt-Assertion")\n'
    "    if cf_jwt:\n"
    '        headers["Cf-Access-Jwt-Assertion"] = cf_jwt\n'
)

idx = src.find(TARGET)
assert idx != -1, f"Patch target not found in {FILE} — OW version may have changed"
assert src.find(TARGET, idx + 1) == -1, (
    f"Multiple '{TARGET.strip()}' occurrences found — patch needs updating"
)

patched = src[:idx] + INSERTION + src[idx:]

with open(FILE, "w") as f:
    f.write(patched)

print(f"OK: CF JWT forwarding patched into {FILE}")
