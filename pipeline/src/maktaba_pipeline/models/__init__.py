"""Model management: catalog, download, storage, and orchestration.

This package owns the real model lifecycle the Go API drives over gRPC:
the :mod:`registry` catalog, the resumable :mod:`downloader`, on-disk
:mod:`storage`, and the :mod:`service` orchestrator that ties them
together and tracks async download jobs.
"""

from __future__ import annotations
