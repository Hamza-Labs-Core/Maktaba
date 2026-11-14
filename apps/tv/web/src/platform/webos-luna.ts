// Story 58.11 — luna, webOS's system services, from the packaged app. LG's webOSTV.js wraps
// them as `webOS.service.request` and `webOS.deviceInfo`; the launcher does not ship that
// library, so it also speaks to the runtime's own PalmServiceBridge, which webOSTV.js itself
// uses (every packaged web app on webOS 3 and later has it). A test or a page with
// webOSTV.js loaded gets `webOS.*` first.

import { log } from "../log";
import type { WebOSDeviceInfo } from "./webos-caps";

export interface LunaRequest {
  method: string;
  parameters?: Record<string, unknown>;
  onSuccess?: (res: Record<string, unknown>) => void;
  onFailure?: (err: Record<string, unknown>) => void;
}

export interface WebOSApi {
  service?: { request(uri: string, req: LunaRequest): unknown };
  deviceInfo?: (cb: (info: WebOSDeviceInfo & { version?: string }) => void) => void;
  platformBack?: () => void;
}

interface PalmBridge {
  onservicecallback: ((msg: string) => void) | null;
  call(uri: string, params: string): void;
  cancel?(): void;
}

declare global {
  interface Window {
    webOS?: WebOSApi;
    PalmSystem?: { platformBack?: () => void; deviceInfo?: string };
    PalmServiceBridge?: new () => PalmBridge;
  }
}

/** How long a luna call may take before it counts as failed. */
export const LUNA_TIMEOUT_MS = 3000;

/** One luna call: its answer, or null on a failure or after LUNA_TIMEOUT_MS. */
export function luna(
  w: Window,
  service: string,
  method: string,
  parameters: Record<string, unknown> = {}
): Promise<Record<string, unknown> | null> {
  return new Promise((resolve) => {
    let done = false;
    const finish = (res: Record<string, unknown> | null, why?: string) => {
      if (done) return;
      done = true;
      clearTimeout(timer);
      if (why) log("error", `webos: ${service}/${method} failed: ${why}`);
      resolve(res);
    };
    const timer = setTimeout(() => finish(null, "timeout"), LUNA_TIMEOUT_MS);
    try {
      const svc = w.webOS?.service;
      if (svc) {
        svc.request(service, {
          method,
          parameters,
          onSuccess: (res) => finish(res),
          onFailure: (err) => finish(null, JSON.stringify(err)),
        });
        return;
      }
      const Bridge = w.PalmServiceBridge;
      if (!Bridge) {
        finish(null, "no luna bridge");
        return;
      }
      const bridge = new Bridge();
      bridge.onservicecallback = (msg: string) => {
        let res: Record<string, unknown> = {};
        try {
          res = JSON.parse(msg) as Record<string, unknown>;
        } catch {
          finish(null, "an answer that is not JSON");
          return;
        }
        if (res.returnValue === false) finish(null, JSON.stringify(res));
        else finish(res);
      };
      bridge.call(`${service.replace(/\/$/, "")}/${method}`, JSON.stringify(parameters));
    } catch (e) {
      finish(null, String(e));
    }
  });
}

const truthy = (v: unknown) => v === true || v === "true";

/**
 * The TV's description: webOS.deviceInfo when webOSTV.js is there, else luna's system
 * properties (model, SDK version, UHD; ACG systemconfig.query). Without webOSTV.js no
 * documented call names HDR10 or Dolby Vision: every LG UHD TV of webOS 5 and later decodes
 * HDR10 and HLG, so a UHD panel counts as HDR10, and Dolby Vision is never claimed (the home
 * then sends the HDR10 base layer).
 */
export async function readDeviceInfo(w: Window): Promise<WebOSDeviceInfo> {
  const info = w.webOS?.deviceInfo;
  if (info) {
    try {
      return await new Promise<WebOSDeviceInfo>((resolve) => info((d) => resolve(d)));
    } catch (e) {
      log("error", `webos: deviceInfo failed: ${String(e)}`);
    }
  }
  const out: WebOSDeviceInfo = {};
  const sys = await luna(w, "luna://com.webos.service.tv.systemproperty", "getSystemInfo", {
    keys: ["modelName", "sdkVersion", "UHD"],
  });
  if (sys) {
    if (typeof sys.modelName === "string") out.modelName = sys.modelName;
    if (typeof sys.sdkVersion === "string") out.sdkVersion = sys.sdkVersion;
    out.uhd = truthy(sys.UHD);
  }
  out.hdr10 = !!out.uhd;
  out.dolbyVision = false;
  return out;
}
