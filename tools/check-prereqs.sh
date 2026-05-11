#!/usr/bin/env bash
# Day-1 prerequisites check (Story 22.8).
#
# Lists every tool a contributor needs and prints OK / MISSING for each.
# Exits non-zero if anything required is missing so `make prereqs`
# fails noisily before the dev tries `make dev` and gets a Docker
# error half a minute later.
#
# Required (`make dev` and `make test` won't work without these):
#     docker, docker compose v2, git, go, uv, pnpm, node, ffmpeg
#
# Recommended (sharper feedback loop, but not strictly required):
#     pre-commit, jq, golangci-lint, shellcheck, goose
#
# Apple Silicon vs Intel: both work; we just record the arch so the
# user knows which doc snippets apply to them (Story 22.8 EC1).

set -uo pipefail

GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
BOLD='\033[1m'
DIM='\033[2m'
NC='\033[0m'

required_missing=0
recommended_missing=0

os=$(uname -s)

# Per-tool install hint, printed right after the ✗ line so the user can
# copy-paste a working command. Linux is Debian/Ubuntu-flavored; other
# distros will need to translate the package name.
install_hint() {
  local name=$1
  case "$name" in
    docker)
      if [ "$os" = "Darwin" ]; then
        printf "    ${DIM}install:${NC} brew install --cask docker\n"
      else
        printf "    ${DIM}install:${NC} sudo apt install docker.io docker-compose-plugin\n"
      fi
      ;;
    "docker compose")
      printf "    ${DIM}install:${NC} ships with Docker Desktop (macOS); on Linux: sudo apt install docker-compose-plugin\n"
      ;;
    git)
      if [ "$os" = "Darwin" ]; then
        printf "    ${DIM}install:${NC} brew install git  (or: xcode-select --install)\n"
      else
        printf "    ${DIM}install:${NC} sudo apt install git\n"
      fi
      ;;
    go)
      if [ "$os" = "Darwin" ]; then
        printf "    ${DIM}install:${NC} brew install go\n"
      else
        printf "    ${DIM}install:${NC} sudo apt install golang-go  (or download from https://go.dev/dl/)\n"
      fi
      ;;
    uv)
      printf "    ${DIM}install:${NC} curl -LsSf https://astral.sh/uv/install.sh | sh\n"
      ;;
    pnpm)
      printf "    ${DIM}install:${NC} corepack enable && corepack prepare pnpm@latest --activate\n"
      ;;
    node)
      if [ "$os" = "Darwin" ]; then
        printf "    ${DIM}install:${NC} brew install node\n"
      else
        printf "    ${DIM}install:${NC} sudo apt install nodejs npm  (or use nvm: https://github.com/nvm-sh/nvm)\n"
      fi
      ;;
    ffmpeg)
      if [ "$os" = "Darwin" ]; then
        printf "    ${DIM}install:${NC} brew install ffmpeg\n"
      else
        printf "    ${DIM}install:${NC} sudo apt install ffmpeg\n"
      fi
      ;;
    pre-commit)
      printf "    ${DIM}install:${NC} uv tool install pre-commit  (or: pip install pre-commit)\n"
      ;;
    jq)
      if [ "$os" = "Darwin" ]; then
        printf "    ${DIM}install:${NC} brew install jq\n"
      else
        printf "    ${DIM}install:${NC} sudo apt install jq\n"
      fi
      ;;
    golangci-lint)
      if [ "$os" = "Darwin" ]; then
        printf "    ${DIM}install:${NC} brew install golangci-lint  (or: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)\n"
      else
        printf "    ${DIM}install:${NC} go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest\n"
      fi
      ;;
    shellcheck)
      if [ "$os" = "Darwin" ]; then
        printf "    ${DIM}install:${NC} brew install shellcheck\n"
      else
        printf "    ${DIM}install:${NC} sudo apt install shellcheck\n"
      fi
      ;;
    goose)
      printf "    ${DIM}install:${NC} go install github.com/pressly/goose/v3/cmd/goose@latest\n"
      ;;
  esac
}

# Print "OK <name> <version>" or "MISSING <name>". version_extract is
# the (possibly noisy) command whose first numeric-looking word is
# treated as the version string. min_version, if non-empty, gets a
# heads-up note (we don't enforce strict semver here — host envs vary).
check() {
  local kind=$1 name=$2 cmd=$3 version_extract=$4 min_version=${5:-}

  if ! command -v "$cmd" >/dev/null 2>&1; then
    if [ "$kind" = required ]; then
      printf "${RED}✗ MISSING${NC} %-18s (required)\n" "$name"
      install_hint "$name"
      required_missing=$((required_missing + 1))
    else
      printf "${YELLOW}○ missing${NC}  %-18s (recommended)\n" "$name"
      install_hint "$name"
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
check recommended "pre-commit"      pre-commit        "pre-commit --version"        ""
check recommended "jq"              jq                "jq --version"                ""
check recommended "golangci-lint"   golangci-lint     "golangci-lint --version"     ""
check recommended "shellcheck"      shellcheck        "shellcheck --version"        ""
check recommended "goose"           goose             "goose -version"              ""

printf "\n"
if [ "$required_missing" -gt 0 ]; then
  printf "${RED}%d required tool(s) missing.${NC} Install them, then re-run 'make prereqs'.\n" "$required_missing"
  printf "Setup notes: see README.md#prerequisites and CONTRIBUTING.md#prerequisites\n"
  exit 1
fi

if [ "$recommended_missing" -gt 0 ]; then
  printf "${YELLOW}%d recommended tool(s) missing.${NC} 'make dev' and 'make test' will work, but the inner loop is sharper with these installed.\n" "$recommended_missing"
fi

printf "${GREEN}All required prerequisites present.${NC} Try 'make dev' next.\n"
