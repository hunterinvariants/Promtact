#!/usr/bin/env bash
# Remove old release directories left behind by deploy-release.sh.
#
# Deployments accumulate: every push that reaches the host leaves a directory
# under releases/, and nothing removes them. On a small VM that is the failure
# mode where a deploy fails at 3am because the disk filled with old copies of
# the thing that was supposed to be deployed.
#
# Two rules make this safe to run unattended:
#
#   - The release the "current" symlink points at is never removed, whatever
#     its age or position in the list.
#   - KEEP releases are retained beyond that, because deploy-release.sh rolls
#     back to the previous release when a deploy fails. Pruning down to one
#     would take the rollback target away.
#
# It prints what it would do and exits. Pass --apply to actually delete.
#
#   scripts/prune-releases.sh                  # show what would go
#   scripts/prune-releases.sh --apply          # do it
#   KEEP=10 scripts/prune-releases.sh --apply  # retain more

set -euo pipefail

TARGET_DIR="${DEPLOY_TARGET_DIR:-/opt/promtact}"
RELEASES_DIR="$TARGET_DIR/releases"
CURRENT_LINK="$TARGET_DIR/current"
KEEP="${KEEP:-5}"
APPLY=0

for arg in "$@"; do
  case "$arg" in
    --apply) APPLY=1 ;;
    -h|--help) sed -n '2,25p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

if [ ! -d "$RELEASES_DIR" ]; then
  echo "no releases directory at $RELEASES_DIR" >&2
  exit 1
fi

case "$KEEP" in
  ''|*[!0-9]*) echo "KEEP must be a number, got: $KEEP" >&2; exit 2 ;;
esac
if [ "$KEEP" -lt 2 ]; then
  # Below two there is no rollback target, which is the one thing this script
  # must not quietly take away.
  echo "KEEP must be at least 2 so a rollback target survives" >&2
  exit 2
fi

CURRENT="$(readlink -f "$CURRENT_LINK" 2>/dev/null || true)"
if [ -z "$CURRENT" ]; then
  # Without knowing what is live, deleting anything here is a guess.
  echo "cannot resolve $CURRENT_LINK; refusing to prune" >&2
  exit 1
fi

# The resolved path must actually be a release. If "current" is not a symlink
# into releases/ — because it was replaced by a real directory, or the link is
# dangling — then readlink still succeeds and returns something that matches no
# release, and every release below the keep count would look safe to delete,
# including the running one. Refuse instead.
case "$CURRENT" in
  "$(readlink -f "$RELEASES_DIR")"/*) ;;
  *)
    echo "$CURRENT_LINK does not point into $RELEASES_DIR (resolved to $CURRENT)" >&2
    echo "refusing to prune: the live release cannot be identified" >&2
    exit 1
    ;;
esac
if [ ! -d "$CURRENT" ]; then
  echo "$CURRENT_LINK points at $CURRENT, which does not exist; refusing to prune" >&2
  exit 1
fi

echo "target:   $TARGET_DIR"
echo "current:  $CURRENT"
echo "keeping:  $KEEP most recent, plus current"
echo

# Newest first by modification time. Release ids are content hashes, so they
# carry no ordering of their own and the timestamp is what we have.
mapfile -t RELEASES < <(find "$RELEASES_DIR" -mindepth 1 -maxdepth 1 -type d -printf '%T@ %p\n' \
  | sort -rn | cut -d' ' -f2-)

total="${#RELEASES[@]}"
echo "found $total releases"

kept=0
removed=0
freed=0
for release in "${RELEASES[@]}"; do
  if [ "$(readlink -f "$release")" = "$CURRENT" ]; then
    echo "  keep    $(basename "$release")  (live)"
    kept=$((kept + 1))
    continue
  fi
  if [ "$kept" -lt "$KEEP" ]; then
    echo "  keep    $(basename "$release")"
    kept=$((kept + 1))
    continue
  fi

  size="$(du -sk "$release" 2>/dev/null | cut -f1)"
  freed=$((freed + ${size:-0}))
  removed=$((removed + 1))
  if [ "$APPLY" -eq 1 ]; then
    rm -rf -- "$release"
    echo "  removed $(basename "$release")"
  else
    echo "  would remove $(basename "$release")"
  fi
done

echo
printf 'kept %d, %s %d releases (%d MB)\n' \
  "$kept" "$([ "$APPLY" -eq 1 ] && echo removed || echo 'would remove')" \
  "$removed" "$((freed / 1024))"

if [ "$APPLY" -eq 0 ] && [ "$removed" -gt 0 ]; then
  echo
  echo "Nothing was deleted. Re-run with --apply to proceed."
fi
