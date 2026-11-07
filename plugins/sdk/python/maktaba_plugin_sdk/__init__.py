"""maktaba-plugin-sdk for Python (Epic 54).

The native mode of Maktaba's own first-party plugins (README §15.4): their stage code runs
in ``maktaba-plugin-runner`` with no database login and reaches the database only through
the plugin API, by the **names** of statements registered with the release
(:mod:`maktaba_plugin_sdk.statements`, :mod:`maktaba_plugin_sdk.native`).
"""

from __future__ import annotations

__all__ = ["__version__"]

__version__ = "1.0"
