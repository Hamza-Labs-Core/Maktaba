// Epic 58 story 58.09 — a big-screen picture signed in by bearer (/tv/ in the launcher): an
// <img> sends no Authorization header, so the home's own pictures are fetched with the token
// and shown from a blob URL; elsewhere the plain URL stays.
import { afterEach, describe, expect, it, onTestFinished, vi } from "vitest";
import { render, waitFor } from "@testing-library/react";
import { setBearerAuth } from "../lib/api";
import { Picture } from "./ui";

const bearer = {
  token: () => "a1",
  refresh: async () => false,
  logoutBody: () => undefined,
  clear: () => undefined,
};

afterEach(() => {
  setBearerAuth(null);
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("Picture", () => {
  it("without a bearer: the plain URL", () => {
    const { container } = render(<Picture src="/api/videos/1/poster" />);
    expect(container.querySelector("img")).toHaveAttribute("src", "/api/videos/1/poster");
  });

  it("with a bearer: fetched with the token, shown from a blob", async () => {
    setBearerAuth(bearer);
    const fetchMock = vi.fn(async () => new Response(new Blob(["png"]), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const create = vi.fn(() => "blob:poster-1");
    const revoke = vi.fn();
    const saved = { create: URL.createObjectURL, revoke: URL.revokeObjectURL };
    URL.createObjectURL = create;
    URL.revokeObjectURL = revoke;
    onTestFinished(() => {
      URL.createObjectURL = saved.create;
      URL.revokeObjectURL = saved.revoke;
    });
    const { container, unmount } = render(<Picture src="/api/videos/1/poster" />);
    expect(container.querySelector("img")).toBeNull();
    await waitFor(() =>
      expect(container.querySelector("img")).toHaveAttribute("src", "blob:poster-1")
    );
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/videos/1/poster");
    expect(init.credentials).toBe("omit");
    expect(init.headers).toEqual({ Authorization: "Bearer a1" });
    unmount();
    expect(revoke).toHaveBeenCalledWith("blob:poster-1");
  });

  it("with a bearer: a refused picture leaves the plain panel", async () => {
    setBearerAuth(bearer);
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("", { status: 404 }))
    );
    const { container } = render(<Picture src="/api/videos/2/poster" />);
    await waitFor(() => expect(container.querySelector(".bs-pic--none")).not.toBeNull());
    expect(container.querySelector("img")).toBeNull();
  });

  it("another origin's picture is left to the browser", () => {
    setBearerAuth(bearer);
    const { container } = render(<Picture src="https://img.example/p.jpg" />);
    expect(container.querySelector("img")).toHaveAttribute("src", "https://img.example/p.jpg");
  });
});
