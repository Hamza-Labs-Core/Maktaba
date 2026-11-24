// What the cloud offers on its sign-in pages (GET /v1/auth/providers): Google, Apple, and the
// Turnstile site key when the bot check is on (story 50.30 §4).
import { useEffect, useState } from "react";
import { api } from "./shared/api";

export interface Providers {
  google: boolean;
  apple: boolean;
  turnstileSiteKey: string;
}

const none: Providers = { google: false, apple: false, turnstileSiteKey: "" };

export function useProviders(): Providers {
  const [p, setP] = useState<Providers>(none);
  useEffect(() => {
    let live = true;
    api
      .get<{ google?: boolean; apple?: boolean; turnstile_site_key?: string }>(
        "/v1/auth/providers",
        { auth: false }
      )
      .then(
        (r) =>
          live &&
          setP({
            google: !!r?.google,
            apple: !!r?.apple,
            turnstileSiteKey: r?.turnstile_site_key ?? "",
          })
      )
      .catch(() => live && setP(none));
    return () => {
      live = false;
    };
  }, []);
  return p;
}

/**
 * useBroadcastOn says whether the cloud has broadcasting on (broadcast.enabled; GET
 * /v1/auth/providers' `broadcast`): null while it asks, false when it is off or the answer
 * fails. Broadcasting is off by default (2026-11-24): the Broadcast pages, their nav link and a
 * home's Viewers link show only while it is on.
 */
export function useBroadcastOn(): boolean | null {
  const [on, setOn] = useState<boolean | null>(null);
  useEffect(() => {
    let live = true;
    api
      .get<{ broadcast?: boolean }>("/v1/auth/providers", { auth: false })
      .then((r) => live && setOn(r?.broadcast === true))
      .catch(() => live && setOn(false));
    return () => {
      live = false;
    };
  }, []);
  return on;
}
