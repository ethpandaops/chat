# open-webui-cf — Open-WebUI with Cloudflare Access JWT forwarding.
#
# Adds three lines to get_headers_and_cookies() in openai.py so that
# the Cf-Access-Jwt-Assertion header present on every browser→OW request
# (injected by CF Access) is forwarded to the upstream model endpoint
# (the Hermes agent). Hermes can then include it in outbound calls to other
# CF-protected devnet resources so those requests are authorised as the
# individual user rather than a shared service account.
#
# OW_TAG must match the appVersion of the open-webui Helm chart in use
# (chart 14.5.0 → appVersion 0.9.5). Bump both together when upgrading.
ARG OW_TAG=0.9.5
FROM ghcr.io/open-webui/open-webui:${OW_TAG}

COPY patch.py /tmp/patch.py
RUN python3 /tmp/patch.py && rm /tmp/patch.py
