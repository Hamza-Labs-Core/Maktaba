"""Cloudflare Stream (Epic 32 story 32.6): remote viewing through the home's own account.

Manifest only, no stages: every Cloudflare call is made by the home's API
(``api/internal/tvremote``), so the workers never see the API token. The token field is
``secret`` and ``home_only``: it is sealed in ``home_secrets`` as ``cf_api_token`` and
:func:`~maktaba_pipeline.extensions.store.load_state` never reads it.
"""

from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING

from ..base import Manifest, SettingField

if TYPE_CHECKING:
    from ...engine.graph import GraphBuilder
    from ...engine.stages.registry import StageImpl

__all__ = ["MANIFEST", "CloudflareStreamExtension", "extension"]

MANIFEST = Manifest(
    name="cloudflare_stream",
    version="1",
    description="Remote viewing of Maktaba's channels through your own Cloudflare Stream account.",
    settings=(
        SettingField("account_id", "Account ID", required=True),
        SettingField(
            "api_token",
            "API token",
            secret=True,
            required=True,
            home_only=True,
            secret_name="cf_api_token",
        ),
        SettingField("customer_code", "Customer subdomain code"),
    ),
)


class CloudflareStreamExtension:
    manifest = MANIFEST

    def register(self, graph: GraphBuilder) -> None:
        """No stages: the home's API does the work."""

    def impls(self) -> Mapping[str, StageImpl]:
        return {}

    def is_configured(self, settings: Mapping[str, str]) -> bool:
        # the token is home-only: the workers only see the account id
        return bool(settings.get("account_id"))


extension = CloudflareStreamExtension()
