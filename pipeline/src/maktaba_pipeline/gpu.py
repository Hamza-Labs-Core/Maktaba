"""This machine's NVIDIA GPUs, as its driver reports them: one probe for the capability
report (``cluster/capabilities.py``), Whisper's GPU checks (``stt/ct2.py``) and the venv
bootstrap's choice of torch build.

NVML first (the driver's own library, ``nvml.dll`` / ``libnvidia-ml.so.1``, through
ctypes: no ``pynvml`` package needed), else ``nvidia-smi`` found as the venv bootstrap
finds it (PATH, then where the Windows driver puts it: a service's or a sign-in task's PATH
may not have it). Light: the standard library only, nothing started on the GPU.

:func:`probe` says why it found none, so a machine that reports no GPU says why in its
log and its capability report instead of an empty list.
"""

from __future__ import annotations

import ctypes
import os
import sys
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from .venv_bootstrap import Gpu, nvidia_gpus

__all__ = ["Gpu", "GpuProbe", "nvml", "nvml_gpus", "probe"]


class NvmlMemory(ctypes.Structure):
    _fields_ = [  # noqa: RUF012 - ctypes' own declaration
        ("total", ctypes.c_ulonglong),
        ("free", ctypes.c_ulonglong),
        ("used", ctypes.c_ulonglong),
    ]


def nvml() -> Any | None:
    """NVML, the NVIDIA driver's own library (``nvml.dll`` in System32, older drivers'
    under NVSMI; ``libnvidia-ml.so.1``), or None without one."""
    names = ["libnvidia-ml.so.1"]
    if sys.platform == "win32":
        pf = os.environ.get("PROGRAMFILES", r"C:\Program Files")
        names = ["nvml.dll", str(Path(pf) / "NVIDIA Corporation" / "NVSMI" / "nvml.dll")]
    for name in names:
        try:
            return ctypes.CDLL(name)
        except OSError:
            continue
    return None


def nvml_gpus(load: Callable[[], Any | None] = nvml) -> tuple[list[Gpu], str | None]:
    """``(the GPUs NVML lists, why none)``."""
    lib = load()
    if lib is None:
        return [], "NVML (the NVIDIA driver's library) is not installed"
    try:
        rc = lib.nvmlInit_v2()
        if rc != 0:
            return (
                [],
                f"NVML did not start (error {rc}: no NVIDIA GPU, or its driver is not loaded)",
            )
        try:
            count = ctypes.c_uint()
            rc = lib.nvmlDeviceGetCount_v2(ctypes.byref(count))
            if rc != 0:
                return [], f"NVML could not count the GPUs (error {rc})"
            version = ctypes.create_string_buffer(80)
            driver = None
            if not lib.nvmlSystemGetDriverVersion(version, ctypes.c_uint(len(version))):
                driver = version.value.decode("ascii", "replace")
            out: list[Gpu] = []
            for i in range(count.value):
                handle = ctypes.c_void_p()
                if lib.nvmlDeviceGetHandleByIndex_v2(ctypes.c_uint(i), ctypes.byref(handle)):
                    continue
                mem = NvmlMemory()
                if lib.nvmlDeviceGetMemoryInfo(handle, ctypes.byref(mem)):
                    continue
                name = ctypes.create_string_buffer(96)
                if lib.nvmlDeviceGetName(handle, name, ctypes.c_uint(len(name))):
                    name.value = b"NVIDIA GPU"
                compute = None
                major, minor = ctypes.c_int(), ctypes.c_int()
                cc = getattr(lib, "nvmlDeviceGetCudaComputeCapability", None)
                if cc is not None and not cc(handle, ctypes.byref(major), ctypes.byref(minor)):
                    compute = (major.value, minor.value)
                out.append(
                    Gpu(
                        name.value.decode("utf-8", "replace"),
                        float(mem.total) / (1 << 30),
                        compute,
                        driver,
                    )
                )
            return out, (None if out else "NVML lists no GPU")
        finally:
            lib.nvmlShutdown()
    except (OSError, AttributeError) as exc:  # an NVML without these entry points
        return [], f"NVML: {type(exc).__name__}: {exc}"


@dataclass(frozen=True)
class GpuProbe:
    """What :func:`probe` found: the GPUs, which source said so (``nvml`` or
    ``nvidia-smi``; None without any), and why none (each source's reason)."""

    gpus: list[Gpu]
    source: str | None
    problem: str | None


def probe(
    nvml_probe: Callable[[], tuple[list[Gpu], str | None]] | None = None,
    smi_probe: Callable[[], tuple[list[Gpu], str | None]] | None = None,
) -> GpuProbe:
    """The NVIDIA GPUs: NVML's answer (:func:`nvml_gpus`), else nvidia-smi's
    (:func:`~.venv_bootstrap.nvidia_gpus`); with none, both reasons."""
    gpus, nvml_why = (nvml_probe or nvml_gpus)()
    if gpus:
        return GpuProbe(gpus, "nvml", None)
    gpus, smi_why = (smi_probe or nvidia_gpus)()
    if gpus:
        return GpuProbe(gpus, "nvidia-smi", None)
    return GpuProbe([], None, f"{nvml_why}; {smi_why}")
