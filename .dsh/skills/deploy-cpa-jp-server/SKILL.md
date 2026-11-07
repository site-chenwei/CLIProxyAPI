---
name: deploy-cpa-jp-server
description: Deploy or roll back CLIProxyAPI (CPA) on the Japan 1C1G server, including release rebase, linux/amd64 build, local patch image rebuild, Compose switch, verification, and rollback. Use when the user asks to deploy this repository to the Japan server, publish a new CPA release there, or roll back the server deployment.
whenToUse: The user says "部署到服务器" / "deploy to the Japan server" for this repository, or asks to upgrade, publish, or roll back the server's CPA release.
---

# Deploy CPA to the Japan server

## Environment

Everything lives in `/opt/cpa-manager-plus` on the Japan server. Read
`~/.dsh/secrets/jp-1c1g-server.md` only for a real deployment; it contains the host,
port, account, and SSH key path. Never print or commit those values.

| Path | Purpose |
| --- | --- |
| `compose.yaml` | `cpamp-cli-proxy-api-1` (CLIProxyAPI) and `cpamp-cpa-manager-plus-1` (management panel) |
| `.env` | `CPA_IMAGE`, `CPA_PORT`, `CPAMP_IMAGE`, `CPAMP_PORT` |
| `cliproxyapi/config.yaml` | Live config, bind-mounted into the container; never overwrite |
| `cliproxyapi/auths/`, `logs/`, `plugins/` | Live state, bind-mounted; never overwrite |
| `local-build/` | Patched binary plus Dockerfile used to build `site-chenwei/cliproxyapi:vX.Y.Z-site.N` |
| `local-build/README.md` | Records the current deployed version and patch commit |

The CPA image is a **local patch image**: it has `cpa.local-build=true` and is based on
`eceasy/cli-proxy-api:latest`. Never delete that base image.

## Non-negotiable rules

- Read credentials only when deploying; never echo the SSH key, config secrets, or auth tokens.
- Keep the working tree clean and build the image from one exact commit.
- Do not modify `cliproxyapi/config.yaml`, `auths/`, `logs/`, or `plugins/` during deployment.
- Tag images as `site-chenwei/cliproxyapi:vX.Y.Z-site.N`. A new upstream release starts at
  `site.1`; rebuilding the same release increments `N`.
- `server-update --only cpa` does not detect an image tag change in `.env` for local images.
  Switch tags with `docker compose up -d --no-deps cli-proxy-api`, then verify.
- Push to the remote only when the user explicitly asks. After a rebase, use
  `git push --force-with-lease`; never plain force push.

## Workflow

1. **Prepare the local branch** — fetch upstream tags, back up the branch, rebase the patch
   branch onto the target release, then run a local build and targeted tests. See runbook §1.
2. **Push (only if requested)** — `git push --force-with-lease origin site/normalizer-omit-body`.
3. **Package and upload** — `git archive` the exact HEAD, upload to the server, and compare
   SHA-256 on both sides. See runbook §3.
4. **Build on the server** — use `golang:1.26-bookworm` with
   `CGO_ENABLED=1 GOOS=linux GOARCH=amd64` and ldflags for `main.Version`, `main.Commit`,
   and `main.BuildDate`. See runbook §4.
5. **Build the local image** — back up the old binary, install the new one in `local-build/`,
   update the Dockerfile labels and README, then
   `docker build -t site-chenwei/cliproxyapi:${VERSION} .`. See runbook §5.
6. **Switch the service** — back up `.env`, set `CPA_IMAGE`, validate with
   `docker compose config --quiet`, and run
   `docker compose up -d --no-deps cli-proxy-api`. See runbook §6.
7. **Verify** — image, restart count, version log, plugin load, binary hash, panel health,
   and API auth probe. Roll back on any failure. See runbook §7 and §8.

## Verification checklist

- [ ] `docker compose ps` shows `cpamp-cli-proxy-api-1` on the new image and `Up`.
- [ ] `docker inspect cpamp-cli-proxy-api-1 --format '{{.RestartCount}}'` returns `0`.
- [ ] Logs contain `CLIProxyAPI Version: <VERSION>, Commit: <SHORT>` and `plugin loaded`;
      no `panic`, `fatal`, or `error` lines.
- [ ] `docker exec cpamp-cli-proxy-api-1 sha256sum /CLIProxyAPI/CLIProxyAPI` matches
      `sha256sum /opt/cpa-manager-plus/local-build/CLIProxyAPI`.
- [ ] `curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8317/v1/models` returns
      `401` (no key was sent; this proves the auth middleware is serving).
- [ ] `curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:18317/health` returns `200`.

## Rollback

1. Restore `/opt/cpa-manager-plus/.env` from the backup made in step 6.
2. `cd /opt/cpa-manager-plus && docker compose up -d --no-deps cli-proxy-api`.
3. Confirm the old image and version in `docker compose ps` and the startup logs.

Keep old images, the old binary, and `.env` backups until the new deployment is stable.

## References

- [Runbook](references/runbook.md) — exact commands for every step.
- [Troubleshooting](references/troubleshooting.md) — known pitfalls and rollback notes.
