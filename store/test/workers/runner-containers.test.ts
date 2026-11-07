// #756: with RUNNER = "containers" the checks hand the package and source to the
// CheckRunner Durable Object (its container: Miniflare has none, so a stand-in answers) and
// take its report as the CI fallback's; a runner that fails blocks C8 with runner_failed.
import { describe, expect, it } from "vitest";
import { type RunnerReport, runChecks } from "../../web/checks";
import type { Env } from "../../web/env";
import { E, buildPackage, component, developer, submit } from "./helpers";

function withRunner(run: (pkg: string, src: string) => Promise<RunnerReport>, seen: string[][]): Env {
  const ns = {
    idFromName: (n: string) => ({ name: n }),
    get: () => ({ run: async (pkg: string, src: string) => (seen.push([pkg, src]), run(pkg, src)) }),
  };
  return { ...(E as unknown as Env), RUNNER: "containers", CHECK_RUNNER: ns as never };
}

async function submitted() {
  const dev = await developer();
  const id = `${dev.prefix}.contained`;
  const pkg = await buildPackage(id, "1.0.0", dev.key, { capabilities: { optional: ["tags.write"] }, files: { "plugin.wasm": component("good") } });
  return { id, sid: await submit(dev, id, "1.0.0", pkg) };
}

const row = (sid: string) =>
  E.DB_CATALOG.prepare("SELECT status, route, runner_state, package_key, source_key FROM submissions WHERE id = ?")
    .bind(sid)
    .first<{ status: string; route: string; runner_state: string; package_key: string; source_key: string }>();

describe("the runner on Cloudflare Containers (#756)", () => {
  it("gets the submission's own files and its report counts as the runner's", async () => {
    const { sid } = await submitted();
    const seen: string[][] = [];
    const report = await runChecks(
      withRunner(async () => ({ c7: { result: "pass", detail: "ClamAV: no hit" }, c8: { result: "pass" }, c10: { result: "pass", reproducible: true } }), seen),
      sid,
    );
    const r = (await row(sid))!;
    expect(seen).toEqual([[r.package_key, r.source_key]]);
    // C7 twice: the static rules here and ClamAV in the runner, as with the CI fallback
    expect([...new Set(report.checks.filter((c) => ["C7", "C8", "C10"].includes(c.id) && c.result === "pass").map((c) => c.id))].sort()).toEqual(["C10", "C7", "C8"]);
    expect(report.runner?.c10.reproducible).toBe(true);
    // a first version still goes to a person; the runner ran, so not for runner_unavailable
    expect(r).toMatchObject({ runner_state: "reported", status: "in_review", route: "human" });
    expect(report.route_reasons).not.toContain("runner_unavailable");
  });

  it("a runner that fails blocks C8 with runner_failed, and the version is refused", async () => {
    const { sid } = await submitted();
    const report = await runChecks(
      withRunner(async () => {
        throw new Error("the runner did not start");
      }, []),
      sid,
    );
    expect(report.checks.find((c) => c.id === "C8")).toMatchObject({ result: "block", code: "runner_failed" });
    expect(report.checks.find((c) => c.id === "C8")?.detail).toContain("the runner did not start");
    expect(await row(sid)).toMatchObject({ runner_state: "reported", status: "rejected", route: "blocked" });
  });
});
