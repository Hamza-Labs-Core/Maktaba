"""GPU device enumeration for per-device locks (Story 6.7).

Best-effort detection — no PyTorch dependency. We probe for CUDA via
``pynvml`` if the library is available, MLX via the host being Apple
Silicon, otherwise return an empty list (CPU-only). The returned list
of :class:`DeviceID` strings is consumed by
:class:`maktaba_pipeline.pipeline.concurrency.ConcurrencyManager` to
build per-device :class:`asyncio.Lock` instances that serialize
GPU-bound stages.

Tests inject ``devices=[...]`` directly into the manager so this
module's hardware dependence never reaches the test surface.
"""

from __future__ import annotations

import platform
from typing import NewType

from ..log import get_logger

__all__ = [
    "DeviceID",
    "enumerate_devices",
]


DeviceID = NewType("DeviceID", str)

_log = get_logger()


def enumerate_devices() -> list[DeviceID]:
    """Return the list of GPU devices visible to this worker process.

    Returned strings are of the form ``"cuda:N"`` or ``"mlx:0"``. An
    empty list means CPU-only — the worker still runs every stage,
    just at the configured CPU concurrency without per-device
    serialization.
    """
    devices: list[DeviceID] = []

    # CUDA via pynvml if installed. Wrapped in try/except so a missing
    # module, a driver mismatch, or a permissions error all fall through
    # to "no CUDA devices".
    try:
        import pynvml  # type: ignore[import-not-found]

        pynvml.nvmlInit()
        try:
            count = pynvml.nvmlDeviceGetCount()
            for i in range(count):
                devices.append(DeviceID(f"cuda:{i}"))
        finally:
            pynvml.nvmlShutdown()
    except Exception:
        _log.debug("pynvml_unavailable")

    # Apple Silicon — single MLX device. Only added when no CUDA
    # devices were detected; on the rare external-GPU-on-a-Mac setup
    # CUDA is the more capable backend.
    if platform.system() == "Darwin" and platform.machine() == "arm64" and not devices:
        devices.append(DeviceID("mlx:0"))

    if not devices:
        _log.info("no_gpu_devices_detected")

    return devices
