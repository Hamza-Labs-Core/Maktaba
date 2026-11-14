// Story 58.09 — Find on this network: a TV has no mDNS API, so the launcher probes its own
// /24. It sends GET http://<ip>:8088/api/discovery/home to each of the 254 addresses, 32 at
// a time, each with a 700 ms timeout (about 6 seconds at worst). An address stops at the
// first port that answers as a home; a home answering on two addresses is listed once.

import { readHome, type FetchLike, type HomeAnswer } from "./discovery";

export interface ProbeOptions {
  fetch: FetchLike;
  /** Requests in flight at once (32). */
  concurrency?: number;
  /** Each request's timeout (700 ms). */
  timeoutMs?: number;
  /** The ports tried on each address, in order (the home's web port, 8088). */
  ports?: number[];
  /** Called as each home is found. */
  onFound?: (home: FoundHome) => void;
  /** Stops the scan early. */
  signal?: { aborted: boolean };
}

export interface FoundHome {
  /** Where it answered (http://<ip>:<port>). */
  base: string;
  answer: HomeAnswer;
}

export const PROBE_CONCURRENCY = 32;
export const PROBE_TIMEOUT_MS = 700;
export const PROBE_PORTS = [8088];

/** The other 253 addresses of ip's /24 (.1 to .254 without ip), or [] for a non-IPv4 ip. */
export function subnetHosts(ip: string): string[] {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(ip.trim());
  if (!m) return [];
  const parts = m.slice(1).map(Number);
  if (parts.some((p) => p > 255)) return [];
  const prefix = parts.slice(0, 3).join(".");
  const out: string[] = [];
  for (let i = 1; i <= 254; i++) if (i !== parts[3]) out.push(`${prefix}.${i}`);
  return out;
}

/** Probes ip's /24 and returns the homes found, in address order, each home once. */
export async function probeSubnet(ip: string, opts: ProbeOptions): Promise<FoundHome[]> {
  const hosts = subnetHosts(ip);
  const concurrency = opts.concurrency ?? PROBE_CONCURRENCY;
  const timeoutMs = opts.timeoutMs ?? PROBE_TIMEOUT_MS;
  const ports = opts.ports ?? PROBE_PORTS;
  const found: (FoundHome | null)[] = new Array(hosts.length).fill(null);
  const seen = new Set<string>();
  let next = 0;

  async function probeHost(i: number): Promise<void> {
    for (const port of ports) {
      if (opts.signal?.aborted) return;
      const base = `http://${hosts[i]}:${port}`;
      const answer = await readHome(base, opts.fetch, timeoutMs);
      if (answer) {
        found[i] = { base, answer };
        if (!seen.has(answer.id)) {
          seen.add(answer.id);
          opts.onFound?.({ base, answer });
        }
        return; // the first home per address
      }
    }
  }

  async function worker(): Promise<void> {
    while (next < hosts.length && !opts.signal?.aborted) {
      const i = next++;
      await probeHost(i);
    }
  }

  const workers: Promise<void>[] = [];
  for (let w = 0; w < Math.min(concurrency, hosts.length); w++) workers.push(worker());
  await Promise.all(workers);

  const out: FoundHome[] = [];
  const listed = new Set<string>();
  for (const f of found) {
    if (f && !listed.has(f.answer.id)) {
      listed.add(f.answer.id);
      out.push(f);
    }
  }
  return out;
}
