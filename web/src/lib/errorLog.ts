// Every error nothing caught — a throw in an event handler or timer, a promise
// rejection no one handled — leaves a line in the console with a stable prefix
// ("[uncaught]"), so troubleshooting has a trail even when the person did not
// opt in to usage data (telemetry/client.ts reports these too, once they do).
//
// The line holds the error's kind, a short message and the first frames,
// never the value it was thrown with: an ApiError (already logged by lib/api
// with its method and path) is named by its status only.
import { ApiError } from "./api";

const MAX_MESSAGE = 200;
const MAX_FRAMES = 3;

/** One short, safe line for what was thrown. */
export function describeThrown(thrown: unknown): string {
  if (thrown instanceof ApiError) return `ApiError (${thrown.status})`;
  if (thrown instanceof Error) {
    const name = /^[A-Za-z_$][\w$.]{0,63}$/.test(thrown.name) ? thrown.name : "Error";
    const message = thrown.message.replace(/\s+/g, " ").slice(0, MAX_MESSAGE);
    const frames = (thrown.stack ?? "")
      .split("\n")
      .map((l) => l.trim())
      .filter((l) => l.startsWith("at ") || /@\S+:\d+/.test(l))
      .slice(0, MAX_FRAMES);
    return [`${name}: ${message}`, ...frames].join("\n  ");
  }
  if (typeof thrown === "string") return thrown.slice(0, MAX_MESSAGE);
  return thrown === null ? "null" : typeof thrown;
}

let installed: (() => void) | null = null;

/** Starts logging uncaught errors and unhandled rejections; returns the undo. */
export function installErrorLog(): () => void {
  if (installed) return installed;
  const onError = (ev: ErrorEvent) => {
    console.error("[uncaught] error", describeThrown(ev.error ?? ev.message));
  };
  const onRejection = (ev: PromiseRejectionEvent) => {
    console.error("[uncaught] promise rejection", describeThrown(ev.reason));
  };
  window.addEventListener("error", onError);
  window.addEventListener("unhandledrejection", onRejection);
  installed = () => {
    window.removeEventListener("error", onError);
    window.removeEventListener("unhandledrejection", onRejection);
    installed = null;
  };
  return installed;
}
