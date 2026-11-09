# Troubleshooting and rollback notes

## `server-update --only cpa` says "镜像 digest 未变"

Known behavior: for local patch images (`cpa.local-build=true`) the registry check is
skipped, and the container comparison walks only the containers currently running. Changing
`CPA_IMAGE` in `.env` does not update any running container, so no difference is detected.
This is not a deployment success signal.

Apply the switch explicitly:

```bash
cd /opt/cpa-manager-plus
docker compose up -d --no-deps cli-proxy-api
```

Do not "fix" this by retagging the new image with the previous tag; keep tags and labels
truthful.

## `server-update` refuses to upgrade CPA because the plugin mount is missing

`server-update` checks that `compose.yaml` mounts `./cliproxyapi/plugins` at
`/CLIProxyAPI/plugins`. Keep that mount. Losing it makes installed plugins disappear and
their configuration stale, which is why the update refuses to continue.

## Docker image build fails: `eceasy/cli-proxy-api:latest` not found

The local patch image is based on that image. Pull it; do not remove it:

```bash
docker pull eceasy/cli-proxy-api:latest
```

## New container is restarting

1. Inspect `docker logs --tail 100 cpamp-cli-proxy-api-1`.
2. Check that `cliproxyapi/config.yaml` exists and is valid YAML.
3. Check that the plugin directory still contains compatible `linux/amd64` plugins.
4. If the cause is not obvious and the previous image is still present, set `CPA_IMAGE` back
   to the previous tag, run `docker compose up -d --no-deps cli-proxy-api`, and report the
   failure (runbook §9).

Old images are deleted automatically after verification (runbook §8); until then the previous
tag remains available for rollback.

## Version or label mismatch

`local-build/Dockerfile` labels must say:

- `cpa.local-build="true"`
- `cpa.upstream-version="vX.Y.Z"` (the upstream release, no `-site.N` suffix)
- `cpa.patch-commit="<short commit>"`

`server-update` compares `cpa.upstream-version` against the latest GitHub release. A suffix
or stale value makes it report the local image as stale ("需重建").

## Health probe returns 404

`http://127.0.0.1:8317/health` returns `404` on this build because CPA has no dedicated
health route. Probe `http://127.0.0.1:8317/v1/models` instead; `8317` is the CPA port
published by `compose.yaml`. A `401` without a bearer token is the expected proof that the
service is alive and auth is enforced.

## Disk pressure

Every successful deployment deletes all old `site-chenwei/cliproxyapi` images automatically
(runbook §8); only the current image remains. Check with `df -h /` and `docker system df`.
Never remove `eceasy/cli-proxy-api:latest` or the current image.
