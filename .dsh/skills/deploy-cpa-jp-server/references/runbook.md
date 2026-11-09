# Deployment runbook

All commands are for bash from the repository root on the local machine. Run them in
order and stop at the first failure. `<TAG>` is the upstream release tag such as `v8.0.59`;
`<VERSION>` is the deployed image tag such as `v8.0.59-site.1`.

Export the connection variables once per shell. Read the secret file only now:

```bash
SECRET="$HOME/.dsh/secrets/jp-1c1g-server.md"
SSH_HOST="$(sed -n 's/^主机：//p' "$SECRET")"
SSH_PORT="$(sed -n 's/^端口：//p' "$SECRET")"
SSH_USER="$(sed -n 's/^账号：//p' "$SECRET")"
SSH_KEY="$(sed -n 's/^密钥：//p' "$SECRET")"
SSH=(ssh -i "$SSH_KEY" -p "$SSH_PORT" -o BatchMode=yes -o ConnectTimeout=12 "$SSH_USER@$SSH_HOST")
SCP=(scp -i "$SSH_KEY" -P "$SSH_PORT" -o BatchMode=yes -o ConnectTimeout=12)
```

## 1. Prepare the local branch

```bash
cd /Users/bill/WorkSpace/CLIProxyAPI
git status --short --branch          # must be clean
git fetch upstream --tags --prune

TAG=v8.0.61                          # set the requested release tag
git rebase "$TAG"                    # keep the branch linear; resolve conflicts before continuing

go build -o test-output ./cmd/server && rm -f test-output
go test ./internal/pluginhost/... ./internal/runtime/executor/... ./internal/translator/...
```

Do not deploy a half-rebased tree. If a conflict is unavoidable, report it instead of
inventing a merge resolution.

## 2. Push the branch (only when requested)

```bash
git push --force-with-lease origin site/normalizer-omit-body
```

Never use plain `--force`; the lease protects against overwriting someone else's push.

## 3. Set local variables and package the source

```bash
SHORT="$(git rev-parse --short=8 HEAD)"
BASE_TAG="$(git describe --tags --abbrev=0)"   # e.g. v8.0.59

# New BASE_TAG starts at site.1. For a rebuild, use the next unused N:
"${SSH[@]}" "docker images --format '{{.Repository}}:{{.Tag}}' | grep '^site-chenwei/cliproxyapi:${BASE_TAG}-site\\.' | sort -V | tail -1"
SITE=1                                        # set the chosen N
VERSION="${BASE_TAG}-site.${SITE}"

git archive --format=tar.gz --prefix="CLIProxyAPI-${SHORT}/" \
  -o "/tmp/CLIProxyAPI-${SHORT}.tar.gz" HEAD
shasum -a 256 "/tmp/CLIProxyAPI-${SHORT}.tar.gz"
"${SCP[@]}" "/tmp/CLIProxyAPI-${SHORT}.tar.gz" \
  "root@${SSH_HOST}:/root/CLIProxyAPI-${SHORT}.tar.gz"
"${SSH[@]}" "sha256sum /root/CLIProxyAPI-${SHORT}.tar.gz"
```

The local and remote SHA-256 must match before building.

## 4. Build the linux/amd64 binary on the server

The build uses the `golang:1.26-bookworm` image; if `server-update` has already cleaned it,
Docker pulls it automatically. The first build downloads Go modules and can take several
minutes.

```bash
"${SSH[@]}" "SHORT=${SHORT} VERSION=${VERSION} bash -s" <<'REMOTE'
set -euo pipefail
BUILD_ROOT="/root/cpa-build-${SHORT}"
mkdir -p "$BUILD_ROOT"
tar -xzf "/root/CLIProxyAPI-${SHORT}.tar.gz" -C "$BUILD_ROOT"
cd "$BUILD_ROOT/CLIProxyAPI-${SHORT}"

export BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
docker run --rm -v "$PWD":/app -w /app \
  -e VERSION -e COMMIT="${SHORT}" -e BUILD_DATE \
  golang:1.26-bookworm \
  bash -c 'CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -buildvcs=false -ldflags="-s -w -X main.Version=$VERSION -X main.Commit=$COMMIT -X main.BuildDate=$BUILD_DATE" -o ./CLIProxyAPI ./cmd/server/'

ls -lh ./CLIProxyAPI
file ./CLIProxyAPI
strings ./CLIProxyAPI | grep -m1 "$VERSION"
REMOTE
```

The resulting binary must be `ELF 64-bit LSB executable, x86-64`.

## 5. Install the binary and build the local patch image

```bash
"${SSH[@]}" "SHORT=${SHORT} VERSION=${VERSION} BASE_TAG=${BASE_TAG} bash -s" <<'REMOTE'
set -euo pipefail
cd /opt/cpa-manager-plus/local-build

install -m 755 "/root/cpa-build-${SHORT}/CLIProxyAPI-${SHORT}/CLIProxyAPI" ./CLIProxyAPI

sed -i \
  "s/cpa.upstream-version=\"[^\"]*\"/cpa.upstream-version=\"${BASE_TAG}\"/; s/cpa.patch-commit=\"[^\"]*\"/cpa.patch-commit=\"${SHORT}\"/" \
  Dockerfile
# Update README's "当前部署", "上游基线", and "commit" lines to the new tag and commit.

docker build -t "site-chenwei/cliproxyapi:${VERSION}" .
docker image inspect "site-chenwei/cliproxyapi:${VERSION}" \
  --format 'IMAGE_ID={{.Id}} LABELS={{json .Config.Labels}}'
REMOTE
```

The Dockerfile must keep `FROM eceasy/cli-proxy-api:latest`. Do not rebuild or delete that
base image.

## 6. Switch the Compose service

```bash
"${SSH[@]}" "VERSION=${VERSION} bash -s" <<'REMOTE'
set -euo pipefail
cd /opt/cpa-manager-plus

echo "previous: $(grep '^CPA_IMAGE=' .env)"
sed -i "s|^CPA_IMAGE=.*|CPA_IMAGE=site-chenwei/cliproxyapi:${VERSION}|" .env
grep '^CPA_IMAGE=' .env

docker compose config --quiet
docker compose up -d --no-deps cli-proxy-api
docker compose ps
REMOTE
```

Use `--no-deps` so the management panel is not recreated by accident. Do not use
`server-update --only cpa` to apply a `.env` image-tag change; it only compares the images
of already-running containers and can report "镜像 digest 未变".

## 7. Verify the deployment

```bash
"${SSH[@]}" 'bash -s' <<'REMOTE'
set -euo pipefail
cd /opt/cpa-manager-plus

docker compose ps --format 'table {{.Name}}\t{{.Image}}\t{{.Status}}'
docker inspect cpamp-cli-proxy-api-1 \
  --format 'RestartCount={{.RestartCount}} Status={{.State.Status}}'

docker logs --since 5m cpamp-cli-proxy-api-1 2>&1 \
  | grep -aE 'Version|plugin loaded|panic|fatal|error' | tail -30

docker exec cpamp-cli-proxy-api-1 sha256sum /CLIProxyAPI/CLIProxyAPI
sha256sum /opt/cpa-manager-plus/local-build/CLIProxyAPI

curl -sS -m 5 -o /dev/null -w 'models=%{http_code}\n' http://127.0.0.1:8317/v1/models || true
curl -sS -m 5 -o /dev/null -w 'panel=%{http_code}\n' http://127.0.0.1:18317/health
REMOTE
```

Expected: the container is `Up`, `RestartCount=0`, the version log matches `<VERSION>` and
`<SHORT>`, the two hashes match, `models=401` without an API key, and `panel=200`.

## 8. Clean all build artifacts

Cleanup runs only after every check in §7 passes. `server-update --only cpa` performs this
cleanup automatically; after a manual deployment run it, or run the commands below. Delete
every old `site-chenwei/cliproxyapi` image, every `/root/cpa-build-*` / `/root/cpa-out*`
build directory (including the one used for this deployment), `/root/.debug`,
`/root/.cache/go-build`, every uploaded `/root/CLIProxyAPI-*.tar.gz` archive, freetier plugin
build outputs, and the Docker build cache; only the version currently running is kept. No
backups are kept. `eceasy/cli-proxy-api:latest` is the local image base and must never be
removed.

```bash
"${SSH[@]}" 'bash -s' <<'REMOTE'
set -euo pipefail
CURRENT="$(docker inspect cpamp-cli-proxy-api-1 --format '{{.Config.Image}}')"
echo "keeping ${CURRENT}"
for img in $(docker images --format '{{.Repository}}:{{.Tag}}' --filter 'reference=site-chenwei/cliproxyapi:*'); do
  if [ "$img" != "$CURRENT" ]; then
    docker rmi "$img"
  fi
done
rm -rf /root/cpa-build-* /root/cpa-out* /root/.debug /root/.cache/go-build /root/freetier-build-*
rm -f /root/CLIProxyAPI-*.tar.gz /root/freetier-src-*.tgz /root/freetier-v*.so /root/freetier-v*.h
for img in $(docker images --format '{{.Repository}}:{{.Tag}}' | grep -E '^(golang|alpine):' || true); do
  docker rmi "$img"
done
docker builder prune -a -f
docker image prune -f
docker images --format '{{.Repository}}:{{.Tag}}' --filter 'reference=site-chenwei/cliproxyapi:*'
REMOTE
```

## 9. Rollback

No image, binary, Dockerfile, README, or `.env` backups are kept. If verification fails
before §8, the previous image still exists: set `CPA_IMAGE` back to the previous tag and run
`docker compose up -d --no-deps cli-proxy-api`, then confirm the old version in
`docker compose ps` and the startup logs.

To roll back after a successful deployment, rebuild the previous version from its git commit
with §1–§5, then switch to the rebuilt tag with §6.
