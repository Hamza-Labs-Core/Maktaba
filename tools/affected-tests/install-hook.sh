#!/usr/bin/env bash
# Installs the affected-tests pre-push hook (.githooks/pre-push) for this clone and all its
# worktrees: a two-line .git/hooks/pre-push that runs the tracked hook, so a change to the
# hook reaches everyone on their next pull. It leaves core.hooksPath alone, so the
# pre-commit framework's .git/hooks/pre-commit keeps working.
#
# usage: tools/affected-tests/install-hook.sh [--force]   (--force replaces another pre-push hook)
set -euo pipefail

hooks="$(git rev-parse --git-common-dir)/hooks"
target="$hooks/pre-push"
marker="# maktaba affected-tests"

if [[ -n "$(git config --get core.hooksPath || true)" ]]; then
  echo "core.hooksPath is set ($(git config --get core.hooksPath)): git reads hooks from there."
  echo "Either unset it, or point it at .githooks:  git config core.hooksPath .githooks"
  exit 1
fi
if [[ -e "$target" ]] && ! grep -q "$marker" "$target" && [[ "${1:-}" != "--force" ]]; then
  echo "$target already exists and is not ours; rerun with --force to replace it"
  exit 1
fi
mkdir -p "$hooks"
cat >"$target" <<EOF
#!/usr/bin/env bash
$marker: runs the tracked hook (tools/affected-tests/install-hook.sh)
exec "\$(git rev-parse --show-toplevel)/.githooks/pre-push" "\$@"
EOF
chmod +x "$target"
echo "installed $target → .githooks/pre-push"
echo "skip once: git push --no-verify; gate only: AFFECTED_TESTS_NO_RUN=1 git push"
