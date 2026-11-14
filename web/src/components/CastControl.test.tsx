// Story 58.06: the player's Cast button and mini controller, against a fake of Google's SDK
// (src/test/fakeCast), in English and Arabic.
import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { I18nProvider } from "../lib/i18n";
import { CastSender, type SenderDeps } from "../lib/cast/sender";
import { fakeCast, type FakeCast } from "../test/fakeCast";
import { CastControl } from "./CastControl";
import { keepServerQueue } from "../lib/playQueue";
import { ApiError } from "../lib/api";

const RELAY = "https://h1.relay.maktaba.tv";
let fc: FakeCast;
let posts: unknown[];

function sender(over: Partial<SenderDeps> = {}) {
  return new CastSender({
    appId: "ABCD1234",
    loadSdk: () => Promise.resolve(fc.api),
    post: <T,>(_p: string, body: unknown) => {
      posts.push(body);
      return Promise.resolve({ grant: "cast_G", expires_at: "2026-11-14T16:00:00Z" } as T);
    },
    get: <T,>() => Promise.resolve({ urls: [RELAY], api_level: 1 } as T),
    win: {
      isSecureContext: true,
      location: { origin: RELAY, protocol: "https:" } as Location,
      navigator: { userAgent: "Chrome/130.0.0.0" } as Navigator,
    },
    inCloud: false,
    lang: () => document.documentElement.lang,
    now: () => Date.parse("2026-11-14T10:00:00Z"),
    setTimer: () => 0,
    clearTimer: () => {},
    ...over,
  });
}

function Player({ s, id }: { s: CastSender; id: string }) {
  const [el, setEl] = useState<HTMLVideoElement | null>(null);
  return (
    <>
      <video data-testid="v" ref={setEl} />
      <CastControl videoId={id} el={el} sender={s} />
    </>
  );
}

function openAt(s: CastSender, lang: "en" | "ar" = "en") {
  localStorage.setItem("mkt:locale", lang);
  function Page() {
    const path = useLocation().pathname;
    const id = path.split("/")[2];
    return (
      <>
        <Player s={s} id={id} />
        <p data-testid="where">{path}</p>
      </>
    );
  }
  render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/videos/a/watch"]}>
        <Routes>
          <Route path="/videos/:videoId/watch" element={<Page />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>
  );
  return screen.getByTestId("v") as HTMLVideoElement;
}

beforeEach(() => {
  // jsdom has no media playback
  HTMLMediaElement.prototype.pause = vi.fn();
  fc = fakeCast();
  posts = [];
  sessionStorage.clear();
  localStorage.clear();
});

describe("CastControl", () => {
  it("shows nothing where casting cannot work (a home at http://)", async () => {
    const s = sender({
      win: {
        isSecureContext: false,
        location: { origin: "http://192.168.1.20:8088", protocol: "http:" } as Location,
        navigator: { userAgent: "Chrome/130.0.0.0" } as Navigator,
      },
    });
    openAt(s);
    await act(() => s.start());
    expect(screen.queryByTestId("mkt-cast")).toBeNull();
    expect(screen.queryByRole("button", { name: "Cast to a TV" })).toBeNull();
  });

  it("casts, pauses the player here and shows the mini controller", async () => {
    const s = sender();
    const v = openAt(s);
    const playing = { paused: false };
    Object.defineProperty(v, "paused", { get: () => playing.paused });
    v.pause = () => {
      playing.paused = true;
    };
    fireEvent.click(await screen.findByRole("button", { name: "Cast to a TV" }));
    const mini = await screen.findByTestId("mkt-cast-mini");
    expect(mini).toHaveTextContent(/Playing on \u2068Living room TV\u2069/);
    expect(posts).toEqual([{ video_id: "a" }]);
    expect(playing.paused).toBe(true);
    act(() => fc.status({ isPaused: false, currentTime: 75, duration: 600 }));
    expect(mini).toHaveTextContent("1:15 / 10:00");
    fireEvent.click(screen.getByRole("button", { name: "Pause on the TV" }));
    expect(fc.controls).toEqual(["playOrPause"]);
    fireEvent.click(screen.getByRole("button", { name: "Stop casting" }));
    expect(fc.session?.ended).toBe(true);
  });

  it("casts the server queue it plays", async () => {
    keepServerQueue({
      id: "q1",
      source: { kind: "library", id: "lib" },
      shuffle: true,
      current_pos: 2,
      version: 1,
      source_done: true,
      total_estimate: 3,
      items: [{ pos: 2, origin: "source", id: "a" }],
    });
    const s = sender();
    openAt(s);
    fireEvent.click(await screen.findByRole("button", { name: "Cast to a TV" }));
    await screen.findByTestId("mkt-cast-mini");
    expect(posts).toEqual([{ queue_id: "q1" }]);
    expect(fc.session!.loads[0].media.customData).toMatchObject({ queue_id: "q1", queue_pos: 2 });
  });

  it("follows the TV to the queue's next video, and takes over where the TV stopped", async () => {
    const s = sender();
    openAt(s);
    fireEvent.click(await screen.findByRole("button", { name: "Cast to a TV" }));
    await screen.findByTestId("mkt-cast-mini");
    act(() =>
      fc.status({
        currentTime: 30,
        mediaInfo: {
          contentId: "b",
          contentType: "video/mp4",
          customData: { video_id: "b", queue_pos: 3 },
        },
      })
    );
    await waitFor(() => expect(screen.getByTestId("where")).toHaveTextContent("/videos/b/watch"));
    act(() => fc.status({ currentTime: 412 }));
    act(() => fc.end());
    const v = screen.getByTestId("v") as HTMLVideoElement;
    await waitFor(() => expect(v.currentTime).toBe(412));
    expect(screen.getByRole("button", { name: "Cast to a TV" })).toBeInTheDocument();
  });

  it("says the TV must be on the same network when none is found", async () => {
    const s = sender();
    openAt(s);
    await screen.findByRole("button", { name: "Cast to a TV" });
    act(() => fc.setCastState("NO_DEVICES_AVAILABLE"));
    fireEvent.click(screen.getByRole("button", { name: "Cast to a TV" }));
    expect(await screen.findByRole("status")).toHaveTextContent("same Wi-Fi network");
    expect(posts).toEqual([]);
  });

  it("speaks Arabic, the TV's name isolated, and hands the receiver lang ar", async () => {
    const s = sender();
    openAt(s, "ar");
    fireEvent.click(await screen.findByRole("button", { name: "البث إلى تلفاز" }));
    const mini = await screen.findByTestId("mkt-cast-mini");
    expect(mini).toHaveTextContent("يُعرض على");
    expect(mini).toHaveTextContent("Living room TV");
    expect(screen.getByRole("button", { name: "إيقاف البث" })).toBeInTheDocument();
    expect(fc.session!.loads[0].media.customData).toMatchObject({ lang: "ar" });
    expect(document.documentElement.dir).toBe("rtl");
  });

  it("says why when the home refuses the grant", async () => {
    const s = sender({
      post: () =>
        Promise.reject(
          new ApiError({ type: "x", title: "forbidden", status: 403, code: "cast_via_cloud" })
        ),
    });
    openAt(s);
    fireEvent.click(await screen.findByRole("button", { name: "Cast to a TV" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("not through Maktaba Cloud");
  });
});
