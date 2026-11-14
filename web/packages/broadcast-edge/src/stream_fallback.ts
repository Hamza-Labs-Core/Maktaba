// The Stream fallback (story 50.21 §5, BROADCAST_DELIVERY = stream, or a publisher's override):
// the edge makes one Stream live input per channel on Maktaba's account (signed URLs required,
// recording automatic), hands the home its RTMPS address (the home pushes one rendition through
// Epic 34's push relay), and redirects players to the Stream manifest with a signed token
// (Epic 34's sign.ts). Viewer tokens, modes, limits, revocations and metering stay the same.
import { importSigningKey, signToken } from "../../tvsite/src/sign";
import type { Env } from "./env";

const API = "https://api.cloudflare.com/client/v4";

export interface LiveInput {
  uid: string;
  rtmps_url: string;
  stream_key: string;
}

export function streamConfigured(env: Env): boolean {
  return !!(
    env.STREAM_ACCOUNT_ID &&
    env.STREAM_API_TOKEN &&
    env.STREAM_CUSTOMER_CODE &&
    env.STREAM_KEY_ID &&
    env.STREAM_KEY_JWK
  );
}

function apiFetch(env: Env, path: string, init: RequestInit): Promise<Response> {
  const req = new Request(`${API}${path}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${env.STREAM_API_TOKEN}`,
      "Content-Type": "application/json",
    },
  });
  return env.STREAM_API ? env.STREAM_API.fetch(req) : fetch(req);
}

/** createLiveInput makes the channel's live input. */
export async function createLiveInput(env: Env, ch: string): Promise<LiveInput> {
  const res = await apiFetch(env, `/accounts/${env.STREAM_ACCOUNT_ID}/stream/live_inputs`, {
    method: "POST",
    body: JSON.stringify({
      meta: { name: `maktaba-broadcast ${env.BROADCAST_ENV} ${ch}` },
      recording: { mode: "automatic", requireSignedURLs: true },
    }),
  });
  const body = (await res.json()) as {
    success?: boolean;
    result?: { uid?: string; rtmps?: { url?: string; streamKey?: string } };
  };
  const r = body.result;
  if (!res.ok || !body.success || !r?.uid || !r.rtmps?.url || !r.rtmps.streamKey)
    throw new Error(`stream live input not created (${res.status})`);
  return { uid: r.uid, rtmps_url: r.rtmps.url, stream_key: r.rtmps.streamKey };
}

let keyPromise: { jwk: string; key: Promise<CryptoKey> } | null = null;

/** manifestURL is the signed Stream manifest for a live input, valid ttl seconds. */
export async function manifestURL(
  env: Env,
  uid: string,
  ttlSeconds: number,
  nowSeconds: number
): Promise<string> {
  const jwk = env.STREAM_KEY_JWK ?? "";
  if (!keyPromise || keyPromise.jwk !== jwk) keyPromise = { jwk, key: importSigningKey(jwk) };
  const token = await signToken(
    await keyPromise.key,
    env.STREAM_KEY_ID ?? "",
    uid,
    ttlSeconds,
    nowSeconds
  );
  return `https://customer-${env.STREAM_CUSTOMER_CODE}.cloudflarestream.com/${token}/manifest/video.m3u8`;
}
