# open-webui-cf

Open-WebUI with a minimal patch to forward `Cf-Access-Jwt-Assertion` from
the browser request to the upstream OpenAI-compatible model endpoint (Hermes).

## Why

When Open-WebUI sits behind Cloudflare Access, CF injects
`Cf-Access-Jwt-Assertion` on every browser request. OW's backend never
forwards this header in its outbound model calls. This overlay adds three
lines to `get_headers_and_cookies()` in `backend/open_webui/routers/openai.py`
so the JWT rides along to the Hermes endpoint, enabling skills to authenticate
against other CF-protected devnet resources as the individual user rather than a
shared service account.

## Build and push

```bash
docker build \
  --build-arg OW_TAG=0.10.2 \
  -t ethpandaops/chat:open-webui-cf-0.10.2 \
  images/open-webui-cf/

docker push ethpandaops/chat:open-webui-cf-0.10.2
```

`OW_TAG` must match the `appVersion` of the `open-webui` dependency in the
[`panda-chat` chart](https://github.com/ethpandaops/ethereum-helm-charts/tree/master/charts/panda-chat)
(chart `15.2.0` → appVersion `0.10.2`). Bump this here and the chart's
`open-webui.image.tag` together when upgrading the dependency.

## CI

[`.github/workflows/build-open-webui-cf.yml`](../../.github/workflows/build-open-webui-cf.yml)
builds and pushes on changes under `images/open-webui-cf/**` (or via
`workflow_dispatch`): `docker.io/ethpandaops/chat:open-webui-cf-<ow_tag>-<sha>`
(+ `…-<ow_tag>`), plus a GHCR private mirror.
