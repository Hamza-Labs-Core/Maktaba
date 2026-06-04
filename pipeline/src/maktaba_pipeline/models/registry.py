"""Model catalog — the source of truth for downloadable models.

Maktaba's heavy models (Whisper for STT, sentence-transformers for
embeddings, pyannote for diarization) all live on the Hugging Face Hub.
This module enumerates the supported set with the metadata the
downloader needs: the HF ``repo_id`` + ``revision``, the concrete files
to fetch, their sizes, and an optional SHA256 for post-download
verification.

URL resolution mirrors the public HF resolve endpoint
(``https://huggingface.co/{repo}/resolve/{revision}/{file}``). The
``huggingface_hub`` library is used at download time for the actual
transfer (auth, redirects, CDN), but the catalog itself stays a pure
data structure so it can be inspected — and tested — without the
library or the network.

Sizes are approximate (whole-file, from the model cards) and exist so
the downloader can run a disk-space precheck and the UI can show a
human-readable size before the download starts. Checksums are optional:
HF serves content-addressed blobs with ETag integrity, so we only pin a
SHA256 when we want an independent guarantee.
"""

from __future__ import annotations

from dataclasses import dataclass

__all__ = [
    "HF_ENDPOINT",
    "MODEL_TYPES",
    "ModelFile",
    "ModelSpec",
    "UnknownModel",
    "get_model",
    "list_models",
    "resolve_url",
]

HF_ENDPOINT = "https://huggingface.co"

# The three model roles the pipeline loads. Mirrors the `type` field the
# Go API exposes (stt | embedding | diarization).
MODEL_TYPES = frozenset({"stt", "embedding", "diarization"})


class UnknownModel(KeyError):
    """Raised by :func:`get_model` for an id not in the catalog."""

    def __init__(self, model_id: str) -> None:
        super().__init__(model_id)
        self.model_id = model_id

    def __str__(self) -> str:  # KeyError quotes its arg; we don't want that.
        return f"unknown model: {self.model_id}"


@dataclass(frozen=True, slots=True)
class ModelFile:
    """One file to fetch for a model.

    ``size_bytes`` is the on-disk size used for the disk-space precheck
    and progress totals. ``sha256`` is an optional lowercase-hex digest
    verified after download; ``None`` means "trust HF's ETag integrity".
    """

    filename: str
    size_bytes: int
    sha256: str | None = None


@dataclass(frozen=True, slots=True)
class ModelSpec:
    """A downloadable model: where it lives and what to fetch."""

    id: str
    type: str  # one of MODEL_TYPES
    name: str
    repo_id: str  # Hugging Face repo, e.g. "mlx-community/whisper-large-v3-mlx"
    revision: str  # branch / tag / commit, e.g. "main"
    files: tuple[ModelFile, ...]
    platform: str  # "apple-silicon" | "cuda,cpu" | "any"
    gated: bool = False  # requires accepting a license / an HF auth token

    @property
    def size_bytes(self) -> int:
        """Total download size across all files."""
        return sum(f.size_bytes for f in self.files)

    @property
    def size_human(self) -> str:
        """Human-readable total size, e.g. ``"3.0 GB"``."""
        return _humanize(self.size_bytes)


def _humanize(n: int) -> str:
    size = float(n)
    for unit in ("B", "KB", "MB", "GB", "TB"):
        if size < 1024.0 or unit == "TB":
            if unit == "B":
                return f"{int(size)} {unit}"
            return f"{size:.1f} {unit}"
        size /= 1024.0
    return f"{size:.1f} TB"  # pragma: no cover - unreachable, loop returns first


# MiB/GiB helpers keep the catalog readable.
def _mib(n: float) -> int:
    return int(n * 1024 * 1024)


def _gib(n: float) -> int:
    return int(n * 1024 * 1024 * 1024)


# The catalog. Sizes are whole-file approximations from the model cards;
# they drive the disk-space precheck and the UI size badge, not exact
# accounting.
_CATALOG: dict[str, ModelSpec] = {
    "mlx-whisper-large-v3": ModelSpec(
        id="mlx-whisper-large-v3",
        type="stt",
        name="MLX Whisper Large v3",
        repo_id="mlx-community/whisper-large-v3-mlx",
        revision="main",
        platform="apple-silicon",
        files=(
            ModelFile("config.json", _mib(0.01)),
            ModelFile("weights.npz", _gib(3.0)),
        ),
    ),
    "faster-whisper-large-v3": ModelSpec(
        id="faster-whisper-large-v3",
        type="stt",
        name="Faster Whisper Large v3",
        repo_id="Systran/faster-whisper-large-v3",
        revision="main",
        platform="cuda,cpu",
        files=(
            ModelFile("config.json", _mib(0.01)),
            ModelFile("tokenizer.json", _mib(2.2)),
            ModelFile("vocabulary.txt", _mib(0.4)),
            ModelFile("model.bin", _gib(2.9)),
        ),
    ),
    "all-minilm-l6-v2": ModelSpec(
        id="all-minilm-l6-v2",
        type="embedding",
        name="all-MiniLM-L6-v2",
        repo_id="sentence-transformers/all-MiniLM-L6-v2",
        revision="main",
        platform="any",
        files=(
            ModelFile("config.json", _mib(0.001)),
            ModelFile("tokenizer.json", _mib(0.7)),
            ModelFile("pytorch_model.bin", _mib(90.0)),
        ),
    ),
    "multilingual-e5-large": ModelSpec(
        id="multilingual-e5-large",
        type="embedding",
        name="Multilingual E5 Large",
        repo_id="intfloat/multilingual-e5-large",
        revision="main",
        platform="any",
        files=(
            ModelFile("config.json", _mib(0.001)),
            ModelFile("tokenizer.json", _mib(17.0)),
            ModelFile("pytorch_model.bin", _gib(2.1)),
        ),
    ),
    "pyannote-diarization-3.1": ModelSpec(
        id="pyannote-diarization-3.1",
        type="diarization",
        name="Pyannote Speaker Diarization 3.1",
        repo_id="pyannote/speaker-diarization-3.1",
        revision="main",
        platform="any",
        gated=True,
        files=(
            ModelFile("config.yaml", _mib(0.01)),
            ModelFile("pytorch_model.bin", _mib(26.0)),
        ),
    ),
}


def list_models() -> list[ModelSpec]:
    """Every model in the catalog, in catalog order."""
    return list(_CATALOG.values())


def get_model(model_id: str) -> ModelSpec:
    """Return the spec for ``model_id`` or raise :class:`UnknownModel`."""
    try:
        return _CATALOG[model_id]
    except KeyError:
        raise UnknownModel(model_id) from None


def resolve_url(spec: ModelSpec, filename: str, *, endpoint: str = HF_ENDPOINT) -> str:
    """Build the HF resolve URL for one file of ``spec``.

    Mirrors ``huggingface_hub.hf_hub_url``; kept local so the catalog is
    inspectable without importing the library.
    """
    return f"{endpoint}/{spec.repo_id}/resolve/{spec.revision}/{filename}"
