"""LAN service discovery for the pipeline (Epic 15).

The pipeline itself does not strictly need mDNS, but the same package
houses the ``probe`` helper used by the desktop installer / web onboarding
to scan the LAN for a Maktaba server before falling back to manual entry.

Two surfaces:

* :class:`ServiceAdvertisement` – the payload an API/streaming process
  registers via mDNS / DNS-SD.
* :class:`LANProbe` – an outbound prober that returns the first responder
  on the LAN that answers ``GET /api/system/version`` within ``timeout``.
"""

from .probe import LANProbe, ProbeResult, ServiceAdvertisement

__all__ = ["LANProbe", "ProbeResult", "ServiceAdvertisement"]
