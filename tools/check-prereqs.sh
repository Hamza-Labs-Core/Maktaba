#!/usr/bin/env bash
# Day-1 prerequisites check (Story 22.8).
#
# Lists every tool a contributor needs and prints OK / MISSING for each.
# Exits non-zero if anything required is missing so `make prereqs`
# fails noisily before the dev tries `make dev` and gets a Docker
# error half a minute later.
#
# Required (`make dev` and `make test` won't work without these):
#     docker, docker compose v2, git, go, uv, pnpm, node
#
# Recommended (sharper feedback loop, but not strictly required):
#     pre-commit, jq, golangci-lint
#
# Apple Silicon vs Intel: both work; we just record the arch so the
# user knows which doc snippets apply to them (Story 22.8 EC1).

set -uo pipefail

GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
BOLD='\033[1m'
NC='\033[0m'

required_missing=0
recommended_missing=0

# Print "OK <name> <version>" or "MISSING <name>". version_extract is
# the (possibly noisy) command whose first numeric-looking word is
# treated as the version string. min_version, if non-empty, gets a
# heads-up note (we don't enforce strict semver here — host envs vary).
is_macos() { [ "$(uname -s)" = "Darwin" ]; }

install_hint() {
  local name=$1
  case "$name" in
    docker|"docker compose")
      is_macos && echo "brew install --cask docker" || echo "sudo apt install docker.io docker-compose-v2" ;;
    git)
      is_macos && echo "xcode-select --install" || echo "sudo apt install git" ;;
    go)
      is_macos && echo "brew install go" || echo "sudo apt install golang-go  # or https://go.dev/dl/" ;;
    uv)
      echo "curl -LsSf https://astral.sh/uv/install.sh | sh" ;;
    pnpm)
      echo "corepack enable && corepack prepare pnpm@latest --activate" ;;
    node)
      is_macos && echo "brew install node" || echo "sudo apt install nodejs  # or https://nodejs.org" ;;
    ffmpeg)
      is_macos && echo "brew install ffmpeg" || echo "sudo apt install ffmpeg" ;;
    pre-commit)
      echo "uv tool install pre-commit  # or pip install pre-commit" ;;
    jq)
      is_macos && echo "brew install jq" || echo "sudo apt install jq" ;;
    golangci-lint)
      is_macos && echo "brew install golangci-lint" || echo "go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest" ;;
    goose)
      echo "go install github.com/pressly/goose/v3/cmd/goose@latest" ;;
    shellcheck)
      is_macos && echo "brew install shellcheck" || echo "sudo apt install shellcheck" ;;
    *)
      echo "see CONTRIBUTING.md" ;;
  esac
}

check() {
  local kind=$1 name=$2 cmd=$3 version_extract=$4 min_version=${5:-}

  if ! command -v "$cmd" >/dev/null 2>&1; then
    if [ "$kind" = required ]; then
      printf "${RED}✗ MISSING${NC} %-18s (required)\n" "$name"
      printf "           install: ${BOLD}%s${NC}\n" "$(install_hint "$name")"
      required_missing=$((required_missing + 1))
    else
      printf "${YELLOW}○ missing${NC}  %-18s (recommended)\n" "$name"
      printf "           install: ${BOLD}%s${NC}\n" "$(install_hint "$name")"
      recommended_missing=$((recommended_missing + 1))
    fi
    return
  fi

  local ver
  ver=$(eval "$version_extract" 2>/dev/null | head -n1 | grep -oE '[0-9]+(\.[0-9]+)+' | head -n1)
  if [ -z "$ver" ]; then
    ver="(unknown version)"
  fi

  if [ -n "$min_version" ]; then
    printf "${GREEN}✓ ok${NC}       %-18s %s   (min: %s)\n" "$name" "$ver" "$min_version"
  else
    printf "${GREEN}✓ ok${NC}       %-18s %s\n" "$name" "$ver"
  fi
}

printf "${BOLD}Maktaba prerequisites check${NC}\n"
printf "host: %s %s\n\n" "$(uname -s)" "$(uname -m)"

printf "${BOLD}Required${NC}\n"
check required "docker"          docker            "docker --version"            "24+"
check required "docker compose"  docker            "docker compose version"      "v2.27+"
check required "git"             git               "git --version"               "2.40+"
check required "go"              go                "go version"                  "1.23+"
check required "uv"              uv                "uv --version"                "0.4+"
check required "pnpm"            pnpm              "pnpm --version"              "9+"
check required "node"            node              "node --version"              "20+"
check required "ffmpeg"          ffmpeg            "ffmpeg -version"             "6+"

printf "\n${BOLD}Recommended${NC}\n"
check recommended "goose"           goose             "goose --version"             ""
check recommended "pre-commit"      pre-commit        "pre-commit --version"        ""
check recommended "jq"              jq                "jq --version"                ""
check recommended "golangci-lint"   golangci-lint     "golangci-lint --version"     ""
check recommended "shellcheck"      shellcheck        "shellcheck --version"        ""

printf "\n"
if [ "$required_missing" -gt 0 ]; then
  printf "${RED}%d required tool(s) missing.${NC} Install them, then re-run 'make prereqs'.\n" "$required_missing"
  printf "Setup notes: see CONTRIBUTING.md#prerequisites\n"
  exit 1
fi

if [ "$recommended_missing" -gt 0 ]; then
  printf "${YELLOW}%d recommended tool(s) missing.${NC} 'make dev' and 'make test' will work, but the inner loop is sharper with these installed.\n" "$recommended_missing"
fi

printf "${GREEN}All required prerequisites present.${NC} Try 'make dev' next.\n"
