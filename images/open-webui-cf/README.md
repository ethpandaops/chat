# open-webui-cf

Open-WebUI with a minimal patch to forward `Cf-Access-Jwt-Assertion` from
the browser request to the upstream OpenAI-compatible model endpoint (Hermes).

## Why

When Open-WebUI sits behind Cloudflare Access, CF injects
`Cf-Access-Jwt-Assertion` on every browser request. OW's backend never
forwards this header in its outbound model calls. This overlay adds three
lines to `get_headers_and_cookies()` in `backend/open_webui/routers/openai.py`
so the JWT rides along to hermes-user-proxy → Hermes, enabling skills to
authenticate against other CF-protected devnet resources as the individual
user rather than a shared service account.

## Build and push

```bash
docker build \
  --build-arg OW_TAG=0.9.5 \
  -t ghcr.io/ethpandaops/open-webui-cf:0.9.5 \
  images/open-webui-cf/

docker push ghcr.io/ethpandaops/open-webui-cf:0.9.5
```

`OW_TAG` must match the `appVersion` of the `open-webui` Helm chart in use
(`charts/org-stack/Chart.yaml` → chart `14.5.0` → appVersion `0.9.5`).
Bump both together when upgrading the chart dependency.

## CI

`.gitea/workflows/build-open-webui-cf.yml` is a placeholder for when this
build is wired into the ethpandaops CI. Until then, build and push manually
with the commands above.
