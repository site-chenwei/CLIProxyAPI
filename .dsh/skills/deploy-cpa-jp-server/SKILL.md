---
name: deploy-cpa-jp-server
description: End-to-end release pipeline for this CLIProxyAPI (CPA) fork on the Japan 1C1G server — merge the new upstream main/release into the site/normalizer-omit-body patch branch (rebase, resolve conflicts, build, targeted tests), force-push the branch, then package it, build a linux/amd64 binary, rebuild the local patch image, switch the Compose service, verify, clean build artifacts, and roll back if needed.
whenToUse: The user says "CPA 已发布 vX.Y.Z，把主分支代码合并过来" / "合并并部署" / "发布新版本" / "deploy to the Japan server" / asks to upgrade, publish, push, or roll back the server's CPA release for this repository.
---

# Ship a CPA release to the Japan server (merge → deploy → push)

This skill owns the full release lifecycle for this fork. The three stages can each run alone when
the user scopes the request, but the default is end-to-end:

- **Stage A — Merge**: rebase the fork branch `site/normalizer-omit-body` onto the new upstream
  release (the target tag is always `upstream/main`'s tip), preserving the fork's own commits and
  then building + running targeted tests locally.
- **Stage B — Push**: `git push --force-with-lease` the rebased branch (a rebase rewrites history, so
  a lease-protected force push is required).
- **Stage C — Deploy**: package the exact HEAD, build a linux/amd64 binary, rebuild the local patch
  image, switch Compose, verify, and clean up.

Scope handling: "只合并" stops after Stage A; "只部署" runs Stage C on the current HEAD; "推送分支"
runs Stage B. When the user names a new release and asks to merge/ship, run A → B → C.

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

The fork branch is `site/normalizer-omit-body`; its own commits are the patches carried on top of
each upstream release (currently a pluginhost response-normalizer omit-body patch plus upstream
performance backports). `upstream` = `router-for-me/CLIProxyAPI`, `origin` = `site-chenwei/CLIProxyAPI`.

## Non-negotiable rules

- Read credentials only when deploying; never echo the SSH key, config secrets, or auth tokens.
- Keep the working tree clean and build the image from one exact commit. Never deploy a
  half-rebased tree.
- Do not modify `cliproxyapi/config.yaml`, `auths/`, `logs/`, or `plugins/` during deployment.
- Tag images as `site-chenwei/cliproxyapi:vX.Y.Z-site.N`. A new upstream release starts at
  `site.1`; rebuilding the same release increments `N`.
- `server-update --only cpa` does not detect an image tag change in `.env` for local images.
  Switch tags with `docker compose up -d --no-deps cli-proxy-api`, then verify.
- **Preserve the fork's patches.** When a rebase conflicts because upstream refactored an area the
  fork patched, do not silently drop the fork change and do not hand-stitch conflict markers.
  Reconstruct the file from the clean upstream version (`git show HEAD:<path>`) and re-apply the
  fork's intent on the new architecture, with tests; if a fork patch genuinely cannot map onto the
  new upstream design, stop and report it instead of fabricating a resolution.
- Push only with `git push --force-with-lease`, and only after confirming the remote ref still
  equals your last-fetched tracking ref. Never use plain `--force`.
- Never keep deployment backups or build artifacts: do not copy the old binary, Dockerfile,
  README, or `.env`. After verification passes, delete every old `site-chenwei/cliproxyapi`
  image, every `/root/cpa-build-*` / `/root/cpa-out*` build directory, `/root/.debug`,
  `/root/.cache/go-build`, uploaded `/root/CLIProxyAPI-*.tar.gz` archives, freetier plugin
  build outputs, and the Docker build cache. Only the current running image and
  `eceasy/cli-proxy-api:latest` are kept.

## Workflow

0. **Determine the target release** — `git fetch upstream --tags --prune`; the target is the newest
   `vX.Y.Z` tag and it must equal `upstream/main`. See runbook §1.
1. **Merge (rebase)** — snapshot a `backup/site-normalizer-omit-body-pre-<TAG>` branch, `git rebase
   <TAG>`, resolve conflicts with the playbook below, then run `gofmt` / `go build` / targeted
   tests. See runbook §1.
2. **Push** — pre-check the remote ref, then `git push --force-with-lease origin
   site/normalizer-omit-body`. See runbook §2.
3. **Package and upload** — `git archive` the exact HEAD, upload to the server, and compare
   SHA-256 on both sides. See runbook §3.
4. **Build on the server** — use `golang:1.26-bookworm` with
   `CGO_ENABLED=1 GOOS=linux GOARCH=amd64` and ldflags for `main.Version`, `main.Commit`,
   and `main.BuildDate`. See runbook §4.
5. **Build the local image** — install the new binary in `local-build/`, update the Dockerfile
   labels and README, then `docker build -t site-chenwei/cliproxyapi:${VERSION} .`. See runbook §5.
6. **Switch the service** — set `CPA_IMAGE`, validate with
   `docker compose config --quiet`, and run `docker compose up -d --no-deps cli-proxy-api`.
   See runbook §6.
7. **Verify** — image, restart count, version log, plugin load, binary hash, panel health,
   and API auth probe. See runbook §7.
8. **Clean all build artifacts** — after verification passes, `server-update --only cpa`
   automatically deletes every old `site-chenwei/cliproxyapi` image, build tool images,
   `/root/cpa-build-*` / `/root/cpa-out*` build directories, `/root/.debug`, Go build cache,
   uploaded source archives, freetier plugin build outputs, and the Docker build cache.
   No backups are kept. See runbook §8.

## Merge conflict playbook (Stage A)

- Prefer a clean linear rebase and always snapshot a backup branch first. Never merge-commit onto
  the patch branch.
- On conflict, rebuild the file from upstream rather than editing the auto-merge: during the rebase
  `HEAD` is the new base, so `git show HEAD:<path> > <path>` gives the clean upstream version; then
  re-apply only the fork's hunks with deliberate edits.
- Classify each fork hunk: **(a)** upstream has since absorbed it → drop the fork hunk;
  **(b)** still orthogonal → re-apply verbatim; **(c)** built on an API upstream has refactored →
  port the behavior onto the new API and update any coupled tests (the common hard case).
- A concrete (c) example: the deferred request-capture logic moved from `helps.logging_helpers.go`
  into `logging.DeferredAPICapture`; the fork's immutable-body optimization had to be re-expressed
  as a new `DeferredAPICapture` method with the coupled unit test rewritten to the new budget model.
- Finish with green `gofmt -l`, `go build ./cmd/server`, and the targeted tests. Confirm the tag is
  an ancestor of HEAD and that exactly the fork's commits sit on top.

## Verification checklist

### Stage A — local merge
- [ ] Working tree clean; `<TAG>` is an ancestor of HEAD.
- [ ] `git rev-list --count <TAG>..HEAD` equals the expected number of fork commits.
- [ ] No conflict markers: `git grep -n '^<<<<<<< \|^>>>>>>> ' -- '*.go'` is empty.
- [ ] `gofmt -l` on changed Go files is empty; `go build -o test-output ./cmd/server` passes.
- [ ] `go test ./internal/pluginhost/... ./internal/runtime/executor/... ./internal/translator/...`
      passes.

### Stage B — push
- [ ] Remote ref equals the tracking ref used for the lease before pushing.
- [ ] After push, `git ls-remote origin refs/heads/site/normalizer-omit-body` equals local `HEAD`.

### Stage C — deploy
- [ ] `docker compose ps` shows `cpamp-cli-proxy-api-1` on the new image and `Up`.
- [ ] `docker inspect cpamp-cli-proxy-api-1 --format '{{.RestartCount}}'` returns `0`.
- [ ] Logs contain `CLIProxyAPI Version: <VERSION>, Commit: <SHORT>` and `plugin loaded`;
      no `panic`, `fatal`, or `error` lines.
- [ ] `docker exec cpamp-cli-proxy-api-1 sha256sum /CLIProxyAPI/CLIProxyAPI` matches
      `sha256sum /opt/cpa-manager-plus/local-build/CLIProxyAPI`.
- [ ] `curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8317/v1/models` returns
      `401` (no key was sent; this proves the auth middleware is serving).
- [ ] `curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:18317/health` returns `200`.
- [ ] Only the current `site-chenwei/cliproxyapi` image remains; no `/root/cpa-build-*`,
      `/root/cpa-out*`, `/root/.debug`, `/root/.cache/go-build`, or
      `/root/CLIProxyAPI-*.tar.gz` build artifacts remain; `docker builder du` is empty
      (runbook §8).

## Rollback

No image, binary, Dockerfile, README, or `.env` backups are kept on the server.

- **Fork branch / merge**: the local backup branch `backup/site-normalizer-omit-body-pre-<TAG>`
  restores the pre-rebase tree (`git reset --hard` it, or re-push it with `--force-with-lease`).
- **Deployment before cleanup**: the previous image still exists — set `CPA_IMAGE` back to the
  previous tag and run `docker compose up -d --no-deps cli-proxy-api`.
- **Deployment after a successful deploy (artifacts cleaned)**: rebuild the previous version from
  its git commit following workflow steps 3–6, then switch to the rebuilt tag.

Confirm the version in `docker compose ps` and the startup logs.

## References

- [Runbook](references/runbook.md) — exact commands for every step.
- [Troubleshooting](references/troubleshooting.md) — known pitfalls, rebase-conflict guidance, and rollback notes.
