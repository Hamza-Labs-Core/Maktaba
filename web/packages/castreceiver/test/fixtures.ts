// The home's answers the receiver tests share.

export const HOME = "https://192-168-1-20.h1.direct.maktaba.test:8444";
export const RELAY = "https://h1.relay.maktaba.test";
export const VIDEO = "00000000-0000-4000-8000-0000580700bb";
export const VIDEO2 = "00000000-0000-4000-8000-0000580700bc";
export const QUEUE = "00000000-0000-4000-8000-0000580700cc";
export const SESSION = "00000000-0000-4000-8000-0000580700dd";

/** A grant whose payload names `exp` (seconds); the signature is never checked here. */
export function grant(expSec = Math.floor(Date.now() / 1000) + 6 * 3600): string {
  const b64 = (o: unknown) => Buffer.from(JSON.stringify(o)).toString("base64url");
  return `cast_${b64({ alg: "RS256" })}.${b64({ aud: "cast", exp: expSec })}.sig`;
}

export const discovery = { id: "h1", urls: [HOME, RELAY], api_level: 1 };

export function session(over: Record<string, unknown> = {}) {
  return {
    session_id: SESSION,
    mode: "remux",
    manifest_url: `${HOME}/stream/${SESSION}/master.m3u8?sig=abc`,
    expires_at: new Date(Date.now() + 3 * 3600_000).toISOString(),
    duration_sec: 5400,
    current_rendition: { codec: "hevc", height: 2160 },
    ...over,
  };
}

export const video = {
  id: VIDEO,
  title: "The Lighthouse Keeper",
  description: "A keeper and his daughter on a far rock.",
  duration_sec: 5400,
};

export const video2 = { id: VIDEO2, title: "Episode Two", duration_sec: 1500 };

export const chapters = [
  { start_sec: 0, end_sec: 600, title: "Arrival" },
  { start_sec: 600, end_sec: 5400, title: "The storm" },
];

export const queue = {
  id: QUEUE,
  current_pos: 0,
  version: 3,
  items: [
    { pos: 0, id: VIDEO, title: "The Lighthouse Keeper", series_name: "Harbour Tales" },
    { pos: 1, id: VIDEO2, title: "Episode Two", series_name: "Harbour Tales" },
  ],
};
