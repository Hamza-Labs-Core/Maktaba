// Story 60.14 part 2: the version the LG and Samsung code screen is captured at. The capture
// builds the launcher as the release does, at the next release's version (the highest vX.Y.Z
// tag plus one patch, as tools/release/next_tag.py's next_patch counts it), or the one given;
// never the unstamped build's "dev". `node --test scripts/capture-tvweb.test.mjs`.
import { test } from "node:test";
import assert from "node:assert/strict";
import { launcherVersion, nextPatch } from "./capture-tvweb.mjs";

test("the next release is the highest vX.Y.Z plus one patch", () => {
  assert.equal(nextPatch(["v0.3.159", "v0.3.161", "v0.3.160"]), "0.3.162");
  // numbers, not text: v0.10.0 is above v0.9.9
  assert.equal(nextPatch(["v0.9.9", "v0.10.0", "v0.2.30"]), "0.10.1");
  assert.equal(nextPatch(["v1.0.0", "v0.99.99"]), "1.0.1");
});

test("pre-releases and other tags are not counted", () => {
  assert.equal(
    nextPatch(["v0.3.161", "v0.4.0-rc1", "v0.5", "launcher-v9.0.0", " v0.3.2 "]),
    "0.3.162"
  );
  assert.throws(() => nextPatch([]), /no vX\.Y\.Z tag/);
  assert.throws(() => nextPatch(["v1.2", "dev"]), /no vX\.Y\.Z tag/);
});

test("the launcher's version: the one given, else the next release's; never dev", () => {
  let asked = 0;
  const tags = () => {
    asked++;
    return ["v0.3.161", "v0.3.160"];
  };
  assert.equal(launcherVersion("1.4.2", tags), "1.4.2");
  assert.equal(asked, 0, "a given version asks for no tags");
  assert.equal(launcherVersion(undefined, tags), "0.3.162");
  for (const bad of ["dev", "v1.4.2", "1.4", "1.04.2", ""])
    assert.throws(() => launcherVersion(bad, tags), /not a release version/);
  assert.throws(() => launcherVersion(undefined, () => []), /no vX\.Y\.Z tag/);
});
