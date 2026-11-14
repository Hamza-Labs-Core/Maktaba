// @vitest-environment node
// Story 58.03: the /tv page forwards a TV's QR (maktaba.tv/tv?c=ABCD2345) to Maktaba Cloud's
// page, carrying the code only when it is one, and nothing else of the address.
import { describe, expect, it } from "vitest";
import { CLOUD_TV, forwardTo, tvCode } from "./tv";

describe("the TV's code", () => {
  it("is read in either shape, in any case", () => {
    expect(tvCode("?c=ABCD2345")).toBe("ABCD2345");
    expect(tvCode("?c=abcd-2345")).toBe("ABCD2345");
    expect(tvCode("c=ABCD%202345")).toBe("ABCD2345");
  });

  it("is refused when it is not one", () => {
    expect(tvCode("")).toBeNull();
    expect(tvCode("?c=")).toBeNull();
    expect(tvCode("?c=ABCD234")).toBeNull();
    expect(tvCode("?c=ABCD23456")).toBeNull();
    expect(tvCode("?c=ABCD-1234")).toBeNull(); // 0 and 1 are not in the alphabet
    expect(tvCode("?c=ABCO2345")).toBeNull(); // nor I, L, O, U
    expect(tvCode("?c=<script>")).toBeNull();
  });
});

describe("the forward", () => {
  it("goes to Maktaba Cloud's page with the code", () => {
    expect(forwardTo("?c=abcd-2345")).toBe("https://app.maktaba.tv/tv?c=ABCD2345");
  });

  it("carries nothing else of the address", () => {
    expect(forwardTo("?c=ABCD2345&next=https://evil.example")).toBe(`${CLOUD_TV}?c=ABCD2345`);
  });

  it("does not happen without a valid code: the page stays, with its link", () => {
    expect(forwardTo("")).toBeNull();
    expect(forwardTo("?next=https://evil.example")).toBeNull();
    expect(forwardTo("?c=javascript:alert(1)")).toBeNull();
  });
});
