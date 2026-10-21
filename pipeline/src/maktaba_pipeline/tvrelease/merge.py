"""How updates to the TV add up in one change (the owner's rule, story 33.12 AC3).

An update to the TV starts a change; every update after it joins that same change
(same id, same number, ``updated_at`` bumped, an ``append`` event naming the update)
until the change is approved. Updates are a channel plan from ``write_channels``, a
schedule regeneration (the ``stale`` flag), a hand edit in the channel editor
(channels, art, filler pools and clips), a TV settings save and a rollback's re-stage.
The open change is the newest ``draft`` no one approved (:func:`.store.open_draft`, the
API's ``Store.OpenDraft``); a change approved, or beyond draft (staged … swapping),
takes no more updates: the next one opens a new change, queued behind it. A change
that fails verification goes back to ``draft`` (the API's controller) and is the open
change again; a draft opened while it was in flight joins it (the API's
``tvchange.MergePending``, the same rules as here).

Merge rules (``old`` = what the change holds, ``new`` = the update joining it; the
API's ``merge.go`` is the same, rule for rule):

* **Two layers of channels.** ``pending["channels"]`` is what the change wants, as the
  editor and the stage job read it. Under it lie a *plan layer* - the newest channel
  plan's definitions, kept in ``pending["plan_channels"]`` - and a *hand layer*: what a
  person (or a re-stage) changed, worked out from the definition saved and the one it
  was made over (the plan's, else the live channel's). A field equal to what lay
  beneath is not a hand edit.
* **A newer plan replaces the older plan's part whole**: its channel definitions, its
  external links and unlinks, its filler pool writes, the change set and the digest.
  The hand layer is then laid over the newer plan field by field: renaming a channel
  by hand survives a newer plan that renumbers it. A channel a plan adds is named
  after the plan's digest; a hand edit of it moves to the newer plan's channel of the
  same plan key (``links``), as the fields the person changed (its art and marks too).
* **Hand edits are never dropped by a plan.** A channel removed by hand stays removed;
  a channel added or edited by hand stays when the newer plan no longer names it, or
  removes it (a person removes it by hand).
* **Newer hand edits win over older ones** field by field for a channel (a removal, or
  a channel added by hand, replaces it whole); art per channel and kind, settings per
  section, filler pools and clips per id.
* **Marks only grow**: forced regenerations (``sched``), ``edited`` and ``follow`` are
  the union of both (one regeneration too many is harmless; one too few airs a stale
  schedule).
* Anything else (a re-stage's ``rollback``, ``streaming``): the newer's when it has it.
* **The same plan is not a new update**: a plan whose digest the change already holds
  writes nothing (the plan did not change).

A change made before this rule kept its plan only in ``channels``; one with a digest
and no ``plan_channels`` reads its channels as its plan layer (:func:`with_plan_layer`).
"""

from __future__ import annotations

from typing import Any

from .snapshot import DEF_COLUMNS, _def

__all__ = [
    "PLAN_CHANNELS",
    "hand_layer",
    "live_defs",
    "merge_pending",
    "plan_pending",
    "with_plan_layer",
]

#: the newest channel plan's definitions in a change's pending edits
PLAN_CHANNELS = "plan_channels"
#: the parts of the pending edits a channel plan owns (replaced by a newer plan)
_PLAN_KEYS = ("links", "unlinks", "pools")
#: the marks that only grow
_UNION_KEYS = ("sched", "edited", "follow")
#: merged by their own rule below
_OWN_KEYS = {"channels", PLAN_CHANNELS, "art", "settings", "filler", *_PLAN_KEYS, *_UNION_KEYS}

# a hand-layer entry: ("removed", None) | ("whole", def) | ("fields", {field: value})
_Hand = tuple[str, Any]


async def live_defs(db: Any) -> dict[str, dict[str, Any]]:
    """The live channels' definitions (what the editor edits over)."""
    cols = ", ".join(DEF_COLUMNS)
    rows = await db.fetch(f"SELECT {cols} FROM channels WHERE lifecycle = 'live'")
    return {str(r["id"]): _def(r) for r in rows}


def plan_pending(pending: dict[str, Any]) -> dict[str, Any]:
    """A channel plan's pending edits with its plan layer named."""
    return {**pending, PLAN_CHANNELS: dict(pending.get("channels") or {})}


def with_plan_layer(pending: dict[str, Any], digest: str | None) -> dict[str, Any]:
    """``pending`` with its plan layer: a change made before the rule that holds a plan
    (a digest) but no ``plan_channels`` has only its plan's channels."""
    if PLAN_CHANNELS in pending or not digest:
        return pending
    return plan_pending(pending)


def hand_layer(pending: dict[str, Any], live: dict[str, Any]) -> dict[str, _Hand]:
    """What a person changed in ``pending`` over its plan layer and the live lineup."""
    plan = pending.get(PLAN_CHANNELS) or {}
    out: dict[str, _Hand] = {}
    for cid, d in (pending.get("channels") or {}).items():
        in_plan = cid in plan
        base = plan.get(cid) if in_plan else live.get(cid)
        if d is None:
            if base is not None:  # else the plan's own removal, or nothing to remove
                out[cid] = ("removed", None)
        elif base is None or (in_plan and cid not in live):
            # added by hand, or over a channel only the plan makes: whole (a field
            # list would have nothing to lie on if the plan went)
            if d != base:
                out[cid] = ("whole", d)
        else:
            fields = {k: v for k, v in d.items() if base.get(k) != v}
            if fields:
                out[cid] = ("fields", fields)
    return out


def _over(old: _Hand | None, new: _Hand) -> _Hand:
    """A newer hand entry over an older one."""
    if old is None or new[0] != "fields":
        return new
    if old[0] == "fields":
        return ("fields", {**old[1], **new[1]})
    if old[0] == "whole":
        return ("whole", {**old[1], **new[1]})
    return new  # removed, then edited over the live channel: the edit


def _compose(plan: dict[str, Any], hand: dict[str, _Hand], live: dict[str, Any]) -> dict[str, Any]:
    out = dict(plan)
    for cid, (kind, v) in hand.items():
        if kind == "removed":
            out[cid] = None
        elif kind == "whole":
            out[cid] = v
        else:
            below = plan.get(cid) if plan.get(cid) is not None else live.get(cid)
            if below is not None:
                out[cid] = {**below, **v}
    return out


def _remap(old: dict[str, Any], new: dict[str, Any]) -> dict[str, str]:
    """Old plan channel id -> the newer plan's id for the same plan key (a channel a
    plan makes is named after the plan's digest: the next plan names it anew)."""
    keys = {
        str(lk["channel_id"]): str(lk["key"])
        for lk in old.get("links") or []
        if isinstance(lk, dict) and lk.get("key") and lk.get("channel_id")
    }
    ids = {
        str(lk["key"]): str(lk["channel_id"])
        for lk in new.get("links") or []
        if isinstance(lk, dict) and lk.get("key") and lk.get("channel_id")
    }
    return {o: ids[k] for o, k in keys.items() if k in ids and ids[k] != o}


def _retarget(
    hand: dict[str, _Hand], old_plan: dict[str, Any], remap: dict[str, str]
) -> dict[str, _Hand]:
    """The hand edits of channels the older plan made, moved to the newer plan's
    channel for the same key: the fields a person changed over the older plan's
    definition."""
    out = dict(hand)
    for o, n in remap.items():
        h = out.pop(o, None)
        if h is None:
            continue
        if h[0] == "whole":
            base = old_plan.get(o) or {}
            fields = {k: v for k, v in h[1].items() if k != "id" and base.get(k) != v}
            if not fields:
                continue
            h = ("fields", fields)
        out[n] = h
    return out


def merge_pending(old: dict[str, Any], new: dict[str, Any], live: dict[str, Any]) -> dict[str, Any]:
    """``new`` joined into ``old`` by the rules of the module docstring."""
    new_plan = PLAN_CHANNELS in new
    plan = dict((new if new_plan else old).get(PLAN_CHANNELS) or {})
    hand = hand_layer(old, live)
    remap = _remap(old, new) if new_plan else {}
    if remap:
        hand = _retarget(hand, old.get(PLAN_CHANNELS) or {}, remap)
        old = {
            **old,
            "art": {remap.get(c, c): v for c, v in (old.get("art") or {}).items()},
            **{
                k: [remap.get(str(x), str(x)) for x in old[k]]
                for k in _UNION_KEYS
                if isinstance(old.get(k), list)
            },
        }
    for cid, h in hand_layer(new, live).items():
        hand[cid] = _over(hand.get(cid), h)
    out = {k: v for k, v in old.items() if k not in _OWN_KEYS}
    out.update({k: v for k, v in new.items() if k not in _OWN_KEYS})
    for k in _PLAN_KEYS:
        src = new if new_plan else old
        if k in src:
            out[k] = src[k]
    if plan or new_plan or PLAN_CHANNELS in old:
        out[PLAN_CHANNELS] = plan
    channels = _compose(plan, hand, live)
    if channels:
        out["channels"] = channels
    for k in _UNION_KEYS:
        ids = {str(x) for x in (old.get(k) or [])} | {str(x) for x in (new.get(k) or [])}
        if ids or k in old or k in new:
            out[k] = sorted(ids)
    art: dict[str, Any] = {}
    for src in (old, new):
        for cid, kinds in (src.get("art") or {}).items():
            art[cid] = {**(art.get(cid) or {}), **(kinds or {})}
    if art:
        out["art"] = art
    settings = {**(old.get("settings") or {}), **(new.get("settings") or {})}
    if settings:
        out["settings"] = settings
    filler: dict[str, Any] = {}
    for part in ("pools", "items"):
        got = {
            **((old.get("filler") or {}).get(part) or {}),
            **((new.get("filler") or {}).get(part) or {}),
        }
        if got:
            filler[part] = got
    if filler:
        out["filler"] = filler
    return out
