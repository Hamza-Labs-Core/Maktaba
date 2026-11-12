#!/bin/sh
# pipeline-deps.sh [--cpu-torch] : the pipeline's CI environment, run from pipeline/.
#
# Every extra, as `uv sync --frozen --all-extras` installs it, but without torch's CUDA
# tree: on Linux x86_64 the stt and search extras pull PyPI's torch, the CUDA 13 build,
# and with it triton, cuda-toolkit and the nvidia-* wheels (several GB, and a cold
# download that ran a unit job past its time limit). No pipeline test runs torch or a GPU
# (tests/conftest.py, tests/stt/test_cohere_cuda.py, tests/search/test_semantic.py use
# stand-ins), so CI leaves them out. Their dependencies (sympy, networkx, ...) stay.
#
# --cpu-torch adds torch's CPU build at the locked version from PyTorch's CPU index, with
# no dependencies: mypy type-checks the modules that import torch (stt/cohere_cuda.py).
#
# CI only. Releases, installers and the venv bootstrap ship the lock as it is
# (deploy/packaging/node/build-release.sh exports it); nothing here changes them.
set -eu

# torch, triton, and every cuda-* / nvidia-* package the lock names
skip=$(sed -nE 's/^name = "(torch|triton|cuda-[a-z0-9-]+|nvidia-[a-z0-9-]+)"$/\1/p' uv.lock | sort -u)
case "$skip" in *torch*) ;; *) echo "::error::uv.lock names no torch: is this pipeline/?" >&2; exit 1 ;; esac

args=""
# shellcheck disable=SC2086 # the names, one word each
for p in $skip; do args="$args --no-install-package $p"; done
# shellcheck disable=SC2086 # one word per package
echo "==> uv sync --frozen --all-extras, leaving out:" $skip
# shellcheck disable=SC2086
uv sync --frozen --all-extras $args

if [ "${1:-}" = "--cpu-torch" ]; then
	# PyPI's torch entry (the lock's other one is the Windows cuda group's, +cu130)
	version=$(awk '/^\[\[package\]\]/ { t = 0; v = "" } /^name = "torch"$/ { t = 1 }
		t && /^version = / { v = $3 } t && /^source = \{ registry = "https:\/\/pypi\.org\/simple" \}$/ { gsub(/"/, "", v); print v; exit }' uv.lock)
	[ -n "$version" ] || { echo "::error::no PyPI torch in uv.lock" >&2; exit 1; }
	echo "==> torch $version, CPU build"
	uv pip install --no-deps "torch==$version" --index-url https://download.pytorch.org/whl/cpu
fi
