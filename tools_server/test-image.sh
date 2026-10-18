#!/usr/bin/env bash
# Validate the actual tools runtime without restarting any service. Supply an
# already built image; building and selecting deployment registries is separate.
set -euo pipefail
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
image_tag=${ORKA_TEST_IMAGE:-orka-tools:workspace-sandbox-review}
validation_dir=$(mktemp -d /tmp/orka-image-validation.XXXXXX)
trap 'rm -rf "$validation_dir"' EXIT
chmod 755 "$validation_dir"
mkdir "$validation_dir/storage"
CGO_ENABLED=0 GOPROXY=off GOSUMDB=off go test -c -o "$validation_dir/tools.test" ./tools_server/tools
CGO_ENABLED=0 GOPROXY=off GOSUMDB=off go test -c -o "$validation_dir/runner.test" ./tools_server/runner
CGO_ENABLED=0 GOPROXY=off GOSUMDB=off go test -c -o "$validation_dir/server.test" ./tools_server/server
runtime_args=(--rm --pull=never --init --user "${HOST_UID:-1000}:${HOST_GID:-1000}"
    --cap-drop ALL --security-opt no-new-privileges:true
    --security-opt "seccomp=$repo_root/tools_server/security/seccomp-bwrap.json"
    --pids-limit 256 --memory 2g
    --mount "type=bind,src=$validation_dir/storage,dst=/workspace"
    -e CODE_BWRAP_PATH=/usr/bin/bwrap -e CODE_SANDBOX_MODE=bwrap
    -e ORKA_REQUIRE_SANDBOX_TEST=1)
# Keep the outer container network enabled as in Compose; inner bwrap must
# independently isolate the host/container loopback listener.
docker run "${runtime_args[@]}" --mount "type=bind,src=$validation_dir/runner.test,dst=/validation/test,readonly" \
    --entrypoint /validation/test "$image_tag" -test.run Sandbox -test.v
docker run "${runtime_args[@]}" --mount "type=bind,src=$validation_dir/tools.test,dst=/validation/test,readonly" \
    -e ORKA_TOOLS_IMAGE_TEST=1 -e ORKA_IMAGE_STORAGE=/workspace \
    --entrypoint /validation/test "$image_tag" -test.run 'TestImageOfficeSandbox|TestExecutionOutcomeContract' -test.v
docker run "${runtime_args[@]}" --mount "type=bind,src=$validation_dir/server.test,dst=/validation/test,readonly" \
    --entrypoint /validation/test "$image_tag" -test.run 'Catalog|CodeTools' -test.v
