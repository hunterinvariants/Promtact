#!/usr/bin/env bash
# Fixed, root-owned deploy entrypoint. The self-hosted runner runs as a non-root
# user and may sudo ONLY this command (see /etc/sudoers.d/promtact-runner). It
# validates its inputs and runs the root-owned deploy script, so the runner can
# trigger a deploy of the built artifacts but cannot perform arbitrary root
# actions. Installed to /usr/local/sbin/promtact-deploy by
# scripts/setup-self-hosted-runner.sh; do not run it from the runner workspace.
set -euo pipefail

src="${1:?usage: promtact-deploy <source-dir> [service] [ready-path] [release-id]}"
service="${2:-promtact}"
ready="${3:-/readyz}"
release_id="${4:-manual}"
deploy_script="/opt/promtact/bin/deploy-release.sh"

case "$service" in
  promtact | promtact@blue | promtact@green) ;;
  *) echo "promtact-deploy: refusing unknown service '$service'" >&2; exit 1 ;;
esac
case "$ready" in
  /readyz | /healthz) ;;
  *) echo "promtact-deploy: refusing ready path '$ready'" >&2; exit 1 ;;
esac
case "$src" in
  /*) ;;
  *) echo "promtact-deploy: source dir must be an absolute path" >&2; exit 1 ;;
esac
if [ ! -x "$src/promtact" ] || [ ! -x "$src/promtactl" ]; then
  echo "promtact-deploy: '$src' does not contain the built binaries" >&2
  exit 1
fi
if [ ! -x "$deploy_script" ]; then
  echo "promtact-deploy: $deploy_script is missing (re-run setup-self-hosted-runner.sh)" >&2
  exit 1
fi

exec env \
  DEPLOY_SOURCE_DIR="$src" \
  DEPLOY_TARGET_DIR="/opt/promtact" \
  DEPLOY_SERVICE="$service" \
  DEPLOY_READY_PATH="$ready" \
  DEPLOY_RELEASE_ID="$release_id" \
  bash "$deploy_script"
