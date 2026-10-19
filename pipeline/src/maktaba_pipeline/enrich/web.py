"""The web part's network: one search backend and the pages its results point to.

Backends (``rules.enrich.search``; keys from the Web search extension, sealed in
``home_secrets``):

* :class:`GoogleSearch` - Google Programmable Search (Custom Search JSON API): an API key
  and a search engine id (``cx``);
* :class:`OllamaWebSearch` - Ollama's web search API (``ollama.com/api/web_search``), for
  an install whose model provider is Ollama and that has an Ollama account key.

Every request goes through a :data:`Transport` (tests pass a fake: no test reaches the
real Google, Ollama or any page). The key is never put in a log line or an error: the
Google key travels in the query string as the API requires, so :func:`redact` scrubs
every message that could carry the URL.

Pages are untrusted: :func:`read_page` opens only http(s) addresses on the default
ports that resolve to public addresses (no loopback, private, link-local or multicast,
checked again on every redirect), reads at most :data:`MAX_PAGE_BYTES`, and keeps only
the visible text.
"""

from __future__ import annotations

import asyncio
import contextlib
import ipaddress
import json
import socket
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass, field
from html.parser import HTMLParser
from typing import Any, Protocol

from .proposal import clean_text

__all__ = [
    "GOOGLE_URL",
    "MAX_PAGE_BYTES",
    "OLLAMA_SEARCH_URL",
    "GoogleSearch",
    "HttpResponse",
    "OllamaWebSearch",
    "SearchBackend",
    "SearchFailed",
    "SearchRateLimited",
    "SearchResult",
    "Transport",
    "html_text",
    "public_address",
    "read_page",
    "redact",
    "urllib_transport",
]

GOOGLE_URL = "https://www.googleapis.com/customsearch/v1"
OLLAMA_SEARCH_URL = "https://ollama.com/api/web_search"
MAX_PAGE_BYTES = 1_500_000
MAX_API_BYTES = 2_000_000
MAX_REDIRECTS = 3
USER_AGENT = "Maktaba-metadata/1 (+https://github.com/Hamza-Labs-Core/Maktaba)"


@dataclass(frozen=True, slots=True)
class HttpResponse:
    status: int
    body: bytes
    headers: Mapping[str, str] = field(default_factory=dict)


#: (method, url, headers, body, timeout seconds, max bytes) -> response. Never follows a
#: redirect itself: a 3xx comes back as it is (read_page follows it after its checks).
Transport = Callable[
    [str, str, Mapping[str, str], bytes | None, float, int], Awaitable[HttpResponse]
]


class SearchFailed(Exception):
    """The backend refused or failed (a wrong key, a bad answer): the web part is skipped."""


class SearchRateLimited(Exception):
    """The backend's own quota or rate limit: the job waits and tries again."""


@dataclass(frozen=True, slots=True)
class SearchResult:
    title: str
    url: str
    snippet: str
    #: the page's text when the backend sends it (Ollama does), else ""
    content: str = ""

    def as_json(self) -> dict[str, str]:
        return {"title": self.title, "url": self.url, "snippet": self.snippet,
                "content": self.content}  # fmt: skip


class SearchBackend(Protocol):
    name: str

    async def search(
        self, query: str, *, num: int, language: str, timeout: float
    ) -> list[SearchResult]: ...


def redact(text: str, *secrets: str) -> str:
    for s in secrets:
        if s:
            text = text.replace(s, "[redacted]").replace(urllib.parse.quote(s, safe=""),
                                                         "[redacted]")  # fmt: skip
    return text


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args: Any, **kwargs: Any) -> None:  # noqa: ARG002
        return None


_OPENER = urllib.request.build_opener(_NoRedirect)


def _do(method: str, url: str, headers: Mapping[str, str], body: bytes | None, timeout: float,
        limit: int) -> HttpResponse:  # fmt: skip
    req = urllib.request.Request(url, data=body, method=method, headers=dict(headers))
    try:
        with _OPENER.open(req, timeout=timeout) as r:  # noqa: S310 - http(s) checked by callers
            data = r.read(limit + 1)
            return HttpResponse(
                int(r.status), data[:limit], {k.lower(): v for k, v in r.headers.items()}
            )
    except urllib.error.HTTPError as e:
        data = b""
        with contextlib.suppress(Exception):
            data = e.read(64_000)
        return HttpResponse(int(e.code), data, {k.lower(): v for k, v in (e.headers or {}).items()})


async def urllib_transport(
    method: str, url: str, headers: Mapping[str, str], body: bytes | None, timeout: float,
    limit: int,
) -> HttpResponse:  # fmt: skip
    return await asyncio.to_thread(_do, method, url, headers, body, timeout, limit)


def _json(resp: HttpResponse, who: str) -> Any:
    try:
        return json.loads(resp.body.decode("utf-8"))
    except (UnicodeDecodeError, ValueError) as exc:
        raise SearchFailed(f"{who} answered something that is not JSON") from exc


def _error_reason(data: Any) -> str:
    if isinstance(data, Mapping):
        err = data.get("error")
        if isinstance(err, Mapping):
            errs = err.get("errors")
            if isinstance(errs, list) and errs and isinstance(errs[0], Mapping):
                return str(errs[0].get("reason") or err.get("status") or "")
            return str(err.get("status") or err.get("message") or "")
        if isinstance(err, str):
            return err
    return ""


_LR = {"ar": "lang_ar", "en": "lang_en", "fr": "lang_fr", "ur": "lang_ur", "tr": "lang_tr",
       "id": "lang_id", "de": "lang_de", "es": "lang_es", "fa": "lang_fa"}  # fmt: skip


class GoogleSearch:
    name = "google"

    def __init__(self, api_key: str, cx: str, *, transport: Transport = urllib_transport,
                 url: str = GOOGLE_URL) -> None:  # fmt: skip
        self._key = api_key
        self.cx = cx
        self.transport = transport
        self.url = url

    async def search(
        self, query: str, *, num: int, language: str, timeout: float
    ) -> list[SearchResult]:
        params = {"key": self._key, "cx": self.cx, "q": query, "num": str(max(1, min(10, num))),
                  "safe": "active"}  # fmt: skip
        lr = _LR.get((language or "").split("-")[0])
        if lr:
            params["lr"] = lr
        url = self.url + "?" + urllib.parse.urlencode(params)
        try:
            resp = await self.transport("GET", url, {"Accept": "application/json",
                                                     "User-Agent": USER_AGENT},
                                        None, timeout, MAX_API_BYTES)  # fmt: skip
        except Exception as exc:  # noqa: BLE001 - the transport's error may name the URL
            raise SearchFailed(redact(f"Google search failed: {type(exc).__name__}: {exc}",
                                      self._key)) from None  # fmt: skip
        data = _json(resp, "Google") if resp.body else {}
        if resp.status == 429 or (resp.status == 403 and "Limit" in _error_reason(data)):
            raise SearchRateLimited("Google: the daily quota or rate limit is reached")
        if resp.status >= 400:
            why = _error_reason(data) or "error"
            raise SearchFailed(redact(f"Google answered HTTP {resp.status} ({why})", self._key))
        out: list[SearchResult] = []
        for it in (data.get("items") or []) if isinstance(data, Mapping) else []:
            if not isinstance(it, Mapping):
                continue
            link = str(it.get("link") or "")
            if not link.startswith(("http://", "https://")):
                continue
            out.append(SearchResult(clean_text(it.get("title"), limit=200), link,
                                    clean_text(it.get("snippet"), limit=500)))  # fmt: skip
        return out[:num]


class OllamaWebSearch:
    name = "ollama"

    def __init__(self, api_key: str, *, transport: Transport = urllib_transport,
                 url: str = OLLAMA_SEARCH_URL) -> None:  # fmt: skip
        self._key = api_key
        self.transport = transport
        self.url = url

    async def search(
        self,
        query: str,
        *,
        num: int,
        language: str,
        timeout: float,  # noqa: ARG002
    ) -> list[SearchResult]:
        body = json.dumps({"query": query, "max_results": max(1, min(10, num))}).encode()
        headers = {"Authorization": f"Bearer {self._key}", "Content-Type": "application/json",
                   "Accept": "application/json", "User-Agent": USER_AGENT}  # fmt: skip
        try:
            resp = await self.transport("POST", self.url, headers, body, timeout, MAX_API_BYTES)
        except Exception as exc:  # noqa: BLE001
            raise SearchFailed(redact(f"Ollama web search failed: {type(exc).__name__}: {exc}",
                                      self._key)) from None  # fmt: skip
        if resp.status == 429:
            raise SearchRateLimited("Ollama web search: rate limited")
        if resp.status >= 400:
            raise SearchFailed(f"Ollama web search answered HTTP {resp.status}")
        data = _json(resp, "Ollama")
        out: list[SearchResult] = []
        for it in (data.get("results") or []) if isinstance(data, Mapping) else []:
            if not isinstance(it, Mapping):
                continue
            link = str(it.get("url") or "")
            if not link.startswith(("http://", "https://")):
                continue
            content = str(it.get("content") or "")
            out.append(SearchResult(clean_text(it.get("title"), limit=200), link,
                                    clean_text(content, limit=500), content))  # fmt: skip
        return out[:num]


# ---- pages -----------------------------------------------------------------------------

_SKIP = {"script", "style", "noscript", "svg", "template", "iframe", "head", "nav", "footer",
         "form", "button", "select", "option"}  # fmt: skip
_BLOCK = {"p", "div", "br", "li", "h1", "h2", "h3", "h4", "h5", "h6", "tr", "section",
          "article", "blockquote", "dd", "dt"}  # fmt: skip


class _Text(HTMLParser):
    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.parts: list[str] = []
        self.title: list[str] = []
        self._skip = 0
        self._in_title = False

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:  # noqa: ARG002
        if tag == "title":
            self._in_title = True
        elif tag in _SKIP:
            self._skip += 1
        elif tag in _BLOCK:
            self.parts.append("\n")

    def handle_endtag(self, tag: str) -> None:
        if tag == "title":
            self._in_title = False
        elif tag in _SKIP and self._skip:
            self._skip -= 1
        elif tag in _BLOCK:
            self.parts.append("\n")

    def handle_data(self, data: str) -> None:
        if self._in_title:
            self.title.append(data)
        elif not self._skip:
            self.parts.append(data)


def html_text(html: str) -> tuple[str, str]:
    """(title, visible text) of an HTML page: no scripts, styles, forms or markup."""
    p = _Text()
    with contextlib.suppress(Exception):  # a broken page keeps what was read
        p.feed(html)
        p.close()
    text = "\n".join(" ".join(line.split()) for line in "".join(p.parts).splitlines())
    lines = [ln for ln in text.splitlines() if len(ln) > 1]
    return " ".join("".join(p.title).split()), "\n".join(lines)


def public_address(host: str) -> bool:
    """True when every address ``host`` resolves to is a public one."""
    try:
        infos = socket.getaddrinfo(host, None, proto=socket.IPPROTO_TCP)
    except (OSError, UnicodeError):
        return False
    if not infos:
        return False
    for info in infos:
        try:
            ip = ipaddress.ip_address(str(info[4][0]).split("%")[0])
        except ValueError:
            return False
        if (ip.is_private or ip.is_loopback or ip.is_link_local or ip.is_multicast
                or ip.is_reserved or ip.is_unspecified):  # fmt: skip
            return False
    return True


def _allowed(url: str, resolve: Callable[[str], bool]) -> bool:
    try:
        u = urllib.parse.urlsplit(url)
    except ValueError:
        return False
    if u.scheme not in ("http", "https") or not u.hostname or u.username or u.password:
        return False
    if u.port not in (None, 80, 443):
        return False
    return resolve(u.hostname)


async def read_page(
    url: str,
    *,
    transport: Transport,
    timeout: float,
    chars: int,
    resolve: Callable[[str], bool] = public_address,
) -> tuple[str, str] | None:
    """(title, text) of a result page, or None when it may not or cannot be read."""
    for _ in range(MAX_REDIRECTS + 1):
        if not await asyncio.to_thread(_allowed, url, resolve):
            return None
        try:
            resp = await transport("GET", url, {"User-Agent": USER_AGENT,
                                                "Accept": "text/html,text/plain;q=0.9"},
                                   None, timeout, MAX_PAGE_BYTES)  # fmt: skip
        except Exception:  # noqa: BLE001 - a page that cannot be read is skipped
            return None
        if resp.status in (301, 302, 303, 307, 308):
            loc = resp.headers.get("location", "")
            if not loc:
                return None
            url = urllib.parse.urljoin(url, loc)
            continue
        if resp.status >= 400:
            return None
        ctype = resp.headers.get("content-type", "text/html").lower()
        if not ctype.startswith(("text/html", "text/plain", "application/xhtml")):
            return None
        charset = "utf-8"
        if "charset=" in ctype:
            charset = ctype.split("charset=", 1)[1].split(";")[0].strip() or "utf-8"
        try:
            raw = resp.body.decode(charset, "replace")
        except LookupError:
            raw = resp.body.decode("utf-8", "replace")
        if ctype.startswith("text/plain"):
            title, text = "", raw
        else:
            title, text = html_text(raw)
        return clean_text(title, limit=200), text[:chars]
    return None


def results_json(results: Sequence[SearchResult]) -> list[dict[str, str]]:
    return [r.as_json() for r in results]


def results_from_json(rows: Any) -> list[SearchResult]:
    out: list[SearchResult] = []
    for r in rows if isinstance(rows, list) else []:
        if isinstance(r, Mapping) and str(r.get("url", "")).startswith(("http://", "https://")):
            out.append(
                SearchResult(
                    str(r.get("title", "")),
                    str(r["url"]),
                    str(r.get("snippet", "")),
                    str(r.get("content", "")),
                )
            )
    return out
