"""The ``plugin_compute`` rules: which language model plugins ask, and how much of the
home's models each plugin may use a day (Epic 54, story 54.21). The pipeline never reads
them; they live here because every rules section's schema
(``api/internal/handlers/rules/schema.json``) is generated from one place. The API's plugin
compute routes read them (``api/internal/plugins/compute.go``, with the same defaults,
checked by a test)."""

from __future__ import annotations

from dataclasses import dataclass

from ..settings import Help, register

__all__ = ["PluginComputeConfig"]

_M = "The model plugins ask"
_Q = "How much each plugin may ask"


@dataclass(frozen=True)
class PluginComputeConfig:
    """The language model plugins ask, and each plugin's daily share of the home's models."""

    #: an Ollama server on this home or the local network; empty: the tag check's server
    url: str = ""
    #: a model that server serves; empty: the tag check's model
    model: str = ""
    #: language model questions one plugin may ask a day
    generate_per_day: int = 2000
    #: texts one plugin may turn into vectors a day
    embed_texts_per_day: int = 50000
    #: how long one question may take before the plugin is told the model is unavailable
    timeout_s: int = 120


register(
    PluginComputeConfig,
    {
        "url": Help(
            _M,
            "Model server for plugins (Ollama)",
            "Empty: the server the tag check asks. It must be this home or a machine on the "
            "local network: a plugin's question is refused when the server is on the internet.",
        ),
        "model": Help(
            _M,
            "Model for plugins",
            "Empty: the tag check's model. Any model the server serves.",
        ),
        "generate_per_day": Help(
            _Q,
            "Questions a day per plugin",
            "How many questions one plugin may ask the language model a day. Over it, the "
            "plugin is told it used its share until the next day.",
            minimum=0,
            maximum=100000,
        ),
        "embed_texts_per_day": Help(
            _Q,
            "Texts a day per plugin",
            "How many texts one plugin may turn into vectors a day with the embedding model.",
            minimum=0,
            maximum=1000000,
        ),
        "timeout_s": Help(
            _Q,
            "Longest question (seconds)",
            "A question that takes longer is ended, and the plugin is told the model is "
            "unavailable.",
            minimum=10,
            maximum=600,
        ),
    },
)
