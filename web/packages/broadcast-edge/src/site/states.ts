// The states a guest can see (story 50.27 §3) and the words for each, from a play or beat answer.
//
// viewerState is the one mapping for the web: the Worker uses it to render a page's first state,
// and the watch page's script carries the same function (client.ts embeds its source), so the
// two can never disagree. It must stay self-contained: no imports, no outside names.

export type ViewerStateName =
  | "live"
  | "tuning"
  | "offline"
  | "off_air"
  | "full"
  | "busy"
  | "geoBlocked"
  | "ended"
  | "inviteUsed"
  | "inviteExpired"
  | "inviteRequired"
  | "revoked"
  | "verify"
  | "dailyLimit"
  | "error";

export interface ViewerState {
  state: ViewerStateName;
  /** the message key (strings.ts); "" for a playing channel with nothing to say */
  key: string;
  /** the player plays (live, tuning while the channel wakes, offline on the slate) */
  plays: boolean;
  /** a "try again" action helps */
  retry: boolean;
}

/**
 * viewerState maps an HTTP status and the edge's JSON answer to a guest state. A 200 carries the
 * channel's own state (`channel_state`: live, waiting, offline_slate, off_air, revoked); an
 * error carries its code (`error`).
 */
export function viewerState(
  status: number,
  body: { error?: unknown; channel_state?: unknown } | null
): ViewerState {
  const s = (state: ViewerStateName, key: string, plays: boolean, retry: boolean) => ({
    state,
    key,
    plays,
    retry,
  });
  if (status >= 200 && status < 300) {
    switch (body && body.channel_state) {
      case "waiting":
        return s("tuning", "broadcast.tuning", true, false);
      case "offline_slate":
        return s("offline", "broadcast.homeOffline", true, false);
      case "off_air":
        return s("off_air", "broadcast.off", false, true);
      case "revoked":
        return s("ended", "broadcast.ended", false, false);
      default:
        return s("live", "", true, false);
    }
  }
  const error = body && typeof body.error === "string" ? body.error : "";
  if (status === 451 || error === "geo_blocked")
    return s("geoBlocked", "broadcast.geoBlocked", false, false);
  switch (error) {
    case "broadcast_full":
      return s("full", "broadcast.full", false, true);
    case "broadcast_wait":
      return s("full", "broadcast.waiting", false, true);
    case "broadcast_ip_limit":
      return s("full", "broadcast.ipLimit", false, true);
    case "rate_limited":
      return s("busy", "broadcast.tooManyTries", false, true);
    case "broadcast_daily_limit":
      return s("dailyLimit", "broadcast.dailyLimit", false, false);
    case "invite_used":
      return s("inviteUsed", "broadcast.invite.used", false, false);
    case "invite_expired":
      return s("inviteExpired", "broadcast.invite.expired", false, false);
    case "invite_revoked":
    case "pass_revoked":
    case "revoked":
    case "session_ended":
      return s("revoked", "broadcast.revoked", false, true);
    case "invite_required":
    case "not_invited":
      return s("inviteRequired", "broadcast.inviteRequired", false, false);
    case "turnstile_required":
      return s("verify", "site.verify", false, false);
    case "broadcast_off":
    case "channel_unknown":
      return s("ended", "broadcast.ended", false, false);
  }
  return s("error", "site.error", false, true);
}
