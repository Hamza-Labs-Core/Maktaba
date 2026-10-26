// @vitest-environment node
import { describe, expect, it } from "vitest";
import parity from "./testdata/parity.json";
import { importSigningKey, signToken } from "./sign";

// The Go side (api/internal/tvremote/cf/parity_test.go) signs the same claims
// with the same test-only key: RS256 is deterministic, so the tokens must be
// identical — the Worker's tokens are exactly the app's, which Stream accepts.
describe("signToken", () => {
  it("matches the app's SignToken byte for byte", async () => {
    const key = await importSigningKey(parity.jwk);
    const tok = await signToken(key, parity.kid, parity.sub, parity.ttl_seconds, parity.now);
    expect(tok).toBe(parity.token);
  });

  it("accepts the JWK as JSON too, and caps the life at 24 h", async () => {
    const key = await importSigningKey(atob(parity.jwk));
    const tok = await signToken(key, parity.kid, "v1", 10 * 24 * 3600, 1000);
    const claims = JSON.parse(atob(tok.split(".")[1].replace(/-/g, "+").replace(/_/g, "/")));
    expect(claims).toEqual({ sub: "v1", kid: parity.kid, exp: 1000 + 24 * 3600, nbf: 970 });
  });
});
