// uploadWithProgress: multipart POST over XHR with the session cookie,
// the CSRF header, progress reports and problem+json errors.
import { describe, it, expect, afterEach, vi } from "vitest";
import { ApiError, uploadWithProgress } from "./api";

class FakeXHR {
  static last: FakeXHR;
  method = "";
  url = "";
  headers: Record<string, string> = {};
  withCredentials = false;
  status = 0;
  statusText = "";
  responseText = "";
  body: unknown;
  upload: {
    onprogress?: (e: { lengthComputable: boolean; loaded: number; total: number }) => void;
  } = {};
  onload?: () => void;
  onerror?: () => void;
  constructor() {
    FakeXHR.last = this;
  }
  open(method: string, url: string) {
    this.method = method;
    this.url = url;
  }
  setRequestHeader(k: string, v: string) {
    this.headers[k] = v;
  }
  send(body: unknown) {
    this.body = body;
  }
  answer(status: number, body: string) {
    this.status = status;
    this.responseText = body;
    this.onload?.();
  }
}

describe("uploadWithProgress", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    document.cookie = "mkt_csrf=; expires=Thu, 01 Jan 1970 00:00:00 GMT";
  });

  it("posts the form with credentials and CSRF, reporting progress", async () => {
    vi.stubGlobal("XMLHttpRequest", FakeXHR);
    document.cookie = "mkt_csrf=tok123";
    const seen: number[] = [];
    const form = new FormData();
    const p = uploadWithProgress<{ id: string }>("/api/channels/c1/art/logo", form, (f) =>
      seen.push(f)
    );
    const x = FakeXHR.last;
    expect(x.method).toBe("POST");
    expect(x.url.endsWith("/api/channels/c1/art/logo")).toBe(true);
    expect(x.withCredentials).toBe(true);
    expect(x.headers["X-Maktaba-CSRF"]).toBe("tok123");
    expect(x.body).toBe(form);
    x.upload.onprogress?.({ lengthComputable: true, loaded: 50, total: 100 });
    x.answer(200, '{"id":"c1"}');
    await expect(p).resolves.toEqual({ id: "c1" });
    expect(seen).toEqual([0.5, 1]);
  });

  it("rejects with the server's problem", async () => {
    vi.stubGlobal("XMLHttpRequest", FakeXHR);
    vi.spyOn(console, "error").mockImplementation(() => {});
    const p = uploadWithProgress("/api/channels/c1/art/logo", new FormData());
    FakeXHR.last.answer(
      415,
      '{"type":"x","title":"not a supported image","status":415,"detail":"not a PNG"}'
    );
    const err = await p.catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(415);
    expect((err as ApiError).problem.detail).toBe("not a PNG");
  });
});
