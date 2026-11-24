// Broadcasting is off by default (2026-11-24, the cloud's broadcast.enabled): the web app shows
// no Broadcast link, no Viewers link and no broadcasting page while the cloud says it is off.
import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderAt, session, stubFetch, user } from "../test/harness";

const HOME = {
  server_id: "11111111-1111-4111-8111-111111111111",
  name: "Kitchen",
  owned: true,
  relay_url: "https://kitchen.relay.maktaba.tv",
};

describe("broadcasting off", () => {
  it("the shell has no Broadcast link and /broadcast is not found", async () => {
    stubFetch({
      "GET /v1/auth/providers": [200, { google: false, apple: false, broadcast: false }],
      "POST /v1/auth/refresh": session(user()),
      "GET /v1/homes": [200, { homes: [HOME] }],
    });
    renderAt("/broadcast");
    expect(await screen.findByRole("link", { name: "Maktaba Cloud" })).toBeInTheDocument();
    expect(await screen.findByText(/doesn't exist/)).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Broadcast" })).toBeNull();
  });

  it("a home's Viewers page is not found and the home shows no Viewers link", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user()),
      "GET /v1/homes": [200, { homes: [HOME] }],
    });
    renderAt(`/homes/${HOME.server_id}/viewers`);
    expect(await screen.findByText(/doesn't exist/)).toBeInTheDocument();
  });

  it("the homes list has no Viewers link", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user()),
      "GET /v1/homes": [200, { homes: [HOME] }],
    });
    renderAt("/homes");
    expect(await screen.findByRole("heading", { name: "Kitchen" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Viewers" })).toBeNull();
  });
});
