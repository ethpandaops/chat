# Handoff — Panda `direct` sandbox backend

## What we're doing

Replacing panda-server's Docker-in-Docker sandbox (`sandbox.backend: docker`) with a `direct` backend that executes Python as a subprocess inside the panda-server container. In Kubernetes, the pod boundary already provides isolation — no need for a nested dockerd + sandbox image.

## Why

- **Dockerd inside the pod was broken**: VFS storage driver on the container's overlayfs rootfs caused `docker pull` to hang on a futex lock. The sandbox image never landed, the startup probe killed the container, infinite restart loop.
- **Overlay2 works on PVC but is complex**: Need `--data-root` on PVC (ext4) for overlay2 to work. The entrypoint was patched for this, but it's still a heavy dependency.
- **No dockerd needed at all**: The pod is already the sandbox. Running Python directly is simpler, faster, and eliminates the privileged sidecar.

## Changes made across 4 repos

### 1. `ethpandaops/panda` — New `direct` backend

**Branch**: `qu0b/panda-client-credentials`
**PR**: [#214](https://github.com/ethpandaops/panda/pull/214)

Files:
- **`pkg/sandbox/direct.go`** (new): `DirectBackend` implementing `sandbox.Service`. Runs Python via `python3 script.py` as a subprocess. No sessions (returns "not supported"). Respects timeout via context. Captures stdout/stderr/exit code.
- **`pkg/sandbox/sandbox.go`**: Added `BackendDirect = "direct"` constant and case in `New()` switch.
- **`pkg/config/config.go`**: Made `sandbox.image` validation conditional — only required for `docker`/`gvisor` backends, not `direct`.

Status: ✨ **Working** — confirmed the backend starts via manual test on devnet-7. Logs show:
```
Starting direct execution backend
Direct execution backend started
Sandbox service started backend=direct
```

### Branch-based deploy loop (no release / no main-merge needed)

The chat image used to pull panda from **release tarballs** (tag-only), which is
why nothing could run off a branch. Fixed end-to-end:

- **chat Dockerfile** now consumes panda from the **published panda images** via
  one `PANDA_REF` arg — CLI `ethpandaops/panda:<ref>`, server `:server-<ref>`,
  sandbox `:sandbox-<ref>` (build stages → `COPY` the static binaries + the
  `/usr/local` python prefix). So a panda **PR build** drops straight in.
- **panda already label-builds**: PR labeled `build` → `build-pr.yaml` publishes
  `pr-<n>-<sha>` images for CLI/server/**sandbox**.
- **chat now label-builds too**: `build-hermes-agent-panda.yml` rewritten —
  builds+pushes on push-to-main / git tag / `workflow_dispatch` / a PR labeled
  `build-docker-image` (same-repo). The label build tags the image by branch
  (`hermes-agent-panda-<branch>`), so a devnet pulls it without a release.

**The loop:** label panda PR #214 `build` → note its `pr-214-<sha>` → `workflow_dispatch`
the chat build with `panda_ref=pr-214-<sha>` (or label a chat PR `build-docker-image`
and set the ref) → set `gen_kubernetes_config_chat_image` to the resulting tag →
commit bal-devnets → ArgoCD. **Validated locally** (mimic panda images of the
branch binaries): the refactored Dockerfile builds and `panda-server` boots
`backend=direct` with the sandbox python importing the analytics stack at uid 10000.

### 2. `ethpandaops/chat` — Simplified entrypoint + sandbox Python env

**Branch**: `main` (pushed directly)

Files:
- **`images/hermes-agent-panda/entrypoint.sh`**: The `panda-stack` path now just runs `panda-server serve` directly. No dockerd startup, no sandbox image pull, no background processes. Prepends `/opt/sandbox/bin` to `PATH` so the `direct` backend's `python3` is the one with the analytics deps.
- **`images/hermes-agent-panda/Dockerfile`**: Dropped the dead `docker.io iptables uidmap` layer and the `DOCKERD_STORAGE_DRIVER` env. **Added the sandbox Python env** via a build stage (`FROM ethpandaops/panda:sandbox-${PANDA_VERSION}`) → `COPY /usr/local /opt/sandbox`. This is the fix for the gap the original handoff missed: `direct` runs executed code with the container's own `python3`, which otherwise has **none** of the analytics stack (pandas/numpy/clickhouse-connect + the `ethpandaops` pkg) the old `docker` sandbox image carried — every `panda execute` would `ImportError`. Added `libgomp1` for numpy/pandas C-extensions.

Status: ✅ Ready & **locally validated.** A relocate-copy build (`hermes-agent-base:2026.6.5` + `libgomp1` + `COPY --from=sandbox /usr/local /opt/sandbox`) confirmed `sys.prefix=/opt/sandbox` and `import pandas, numpy, clickhouse_connect` + `from ethpandaops import clickhouse, dora` all succeed. Worth a build-time smoke `import` in CI to keep it gated.

### 3. `ethpandaops/ethereum-helm-charts` — Chart cleanup

**Branch**: `qu0b/panda-chat-stable-bearer`
**PR**: [#483](https://github.com/ethpandaops/ethereum-helm-charts/pull/483)

Files:
- **`templates/configmap.yaml`**: panda config uses `backend: direct` — no image, no network, no sandbox_url.
- **`templates/deployment.yaml`**: panda-server container is now **unprivileged** (`runAsUser: 10000`, caps dropped). No dockerd storage driver env. **startupProbe budget raised to 600s** (`periodSeconds 10 × failureThreshold 60`) — the prior 60s killed panda-server mid-embed (cold-start EIP index build is 3-5 min) → CrashLoop.
- **`templates/service.yaml`**: Added `publishNotReadyAddresses: true`.
- **`values.yaml`**: Removed `sandboxImage` and `storageDriver`. Lower resource limits (no dockerd overhead).
- **Doc-rot swept**: deployment/values/NOTES.txt/README(.gotmpl) no longer claim "privileged"/"dockerd"; README regenerated via helm-docs. `helm lint` clean.

### 3b. `ansible-collection-general` — render breakage fixed

- **`templates/chat.yaml.j2`**: removed the dangling `panda.storageDriver: "{{ gen_kubernetes_config_chat_panda_storage_driver }}"` line — the var was deleted from `defaults/main.yaml` (step 4) but the template still referenced it, so `generate_kubernetes_config` would die on an undefined variable.

### 4. `ethpandaops/ansible-collection-general` — Defaults cleanup

**Branch**: `qu0b/chat-panda-bot-token`
**PR**: [#548](https://github.com/ethpandaops/ansible-collection-general/pull/548)

Files:
- **`roles/generate_kubernetes_config/defaults/main.yaml`**: Removed `gen_kubernetes_config_chat_panda_storage_driver` variable. Updated comment.

## What's remaining

### High priority

1. **Panda PR needs review and merge** (#214) — The `direct` backend is the core dependency. Without a new panda release that includes it, nothing else works.
   - Needs a panda release tag (e.g., v0.33.0) with both `client_credentials` auth and `direct` backend
   - Then bump `PANDA_VERSION` in the chat Dockerfile

2. **Chat image needs to be rebuilt** — The entrypoint change is on main but no new image has been built with it. CI (.github/workflows/build-hermes-agent-panda.yml) should trigger on push to main.
   - Ensure the CI picks up the panda release with the `direct` backend
   - Output: `docker.io/ethpandaops/chat:hermes-agent-panda-2026.6.5-<sha>`

3. **Deploy and test end-to-end**:
   - Update devnet-7 inventory in `bal-devnets` with the new image tag
   - Run `generate_kubernetes_config` ansible role from `platform` repo
   - Verify:
     - Pod becomes Ready quickly (< 30s)
     - Open-WebUI shows `hermes-agent` model
     - Chat works (LLM queries)
     - Panda analytics skill works (Python execution via CLI)

### Medium priority

4. **Merge chart PR** (#483) — After testing, merge and publish as panda-chart 0.3.0+
5. **Merge ansible PR** (#548) — Remove the stale storage driver default
6. ~~**Clean up Dockerfile**~~ — DONE (dockerd packages removed, sandbox env added).
9. ~~**`direct` backend leaks the bot credential into executed code**~~ — FIXED (`panda/pkg/sandbox/direct.go`, branch `qu0b/panda-client-credentials`). `Execute` no longer seeds from `os.Environ()`; it mirrors the docker backend — `SandboxEnvDefaults()` + a non-sensitive passthrough allowlist (`directEnvPassthrough`: PATH/locale/TLS) + `req.Env` (which already carries `ETHPANDAOPS_API_URL` + a scoped per-execution token from `BuildSandboxEnv`). `PANDA_BOT_*` is now withheld. Note: running the subprocess as a different/unprivileged UID does **not** fix this — env is inherited by value regardless of UID; only controlling `cmd.Env` does. Regression test: `TestDirectBackendWithholdsProcessSecrets` in `direct_test.go`.

### Low priority / nice to have

7. **Remove panda-server sidecar entirely** — Once the `direct` backend is stable, consider merging panda-server into the Hermes container as an unprivileged process. Requires moving the bot credential management (client_credentials auth) to a different pattern.
8. **Upstream: Kubernetes Jobs backend** — The real replacement for dockerd would be a Kubernetes Jobs backend in panda-server (using `agent-sandbox` or direct Job creation), but the `direct` backend covers the use case for now.

## Known issues

- **panda-server embedding index is slow**: The proxy's Redis cache returns connection refused (`10.43.190.181:6379: connect: connection refused`), causing panda-server to re-embed all examples/EIPs on every startup. This delays health endpoint availability by ~3-5 minutes. This is a pre-existing infrastructure issue, not caused by the `direct` backend.
- **panda-server health endpoint**: Only responds after all modules are initialized + embedding is built. The startup probe now budgets 600s (`periodSeconds 10 × failureThreshold 60`) to cover the 3-5 min cold embed; the local vector cache (`storage.cache_dir` defaults to `/opt/data/cache`, PVC-backed) should make warm restarts fast, so the long budget mostly bites on a cold PVC. If the proxy Redis stays down and every boot re-embeds, raise it further or fix the cache path.

## How to test

After the panda release is cut and the image is built:

```bash
# 1. Update bal-devnets inventory
#    ansible/inventories/devnet-7/group_vars/all/chat.yaml
#    Set gen_kubernetes_config_chat_image to the new tag

# 2. Render the chart from platform repo
cd ~/repos/ethpandaops/platform
ansible-playbook ...  # generate kubernetes config

# 3. Verify the pod
kubectl get pods -n bal-devnet-7 -l app.kubernetes.io/component=agent
# Expected: 2/2 Ready within 30s

# 4. Verify panda-server health
kubectl exec -n bal-devnet-7 deploy/chat-hermes -c panda-server -- \
  curl -sf http://127.0.0.1:2480/health

# 5. Test Python execution
kubectl exec -n bal-devnet-7 deploy/chat-hermes -c panda-server -- \
  sh -c 'echo "print(42)" | panda execute --code -'

# 6. Open chat.bal-devnet-7.ethpandaops.io and confirm model selector shows "hermes-agent"
```

## Architecture diagram (final)

```
┌─────────────────────────────────────────────────────┐
│ Pod                                                 │
│                                                     │
│  ┌──── hermes (unpriv, uid 10000) ──────────────┐   │
│  │  hermes gateway run          :8642            │   │
│  │  panda CLI → panda-server on 127.0.0.1:2480  │   │
│  └──────────────────────────────────────────────┘   │
│                          │                           │
│  ┌──── panda-server (unpriv, uid 10000) ────────┐   │
│  │  panda-server serve         :2480            │   │
│  │  Sandbox: direct (subprocess)                │   │
│  │  Auth: client_credentials (PANDA_BOT_*)      │   │
│  └──────────────────────────────────────────────┘   │
│                                                     │
│  /opt/data (PVC, ext4)                              │
│    ├── config.yaml (hermes)                         │
│    ├── .config/panda/config.yaml                    │
│    └── panda-storage/                               │
└─────────────────────────────────────────────────────┘
```

**No dockerd, no sandbox image, no privileged container.**
