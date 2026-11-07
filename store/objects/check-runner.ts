// CheckRunner (story 54.16 §1, #756): the isolated runner on Cloudflare Containers. One
// instance per submission starts the check-runner image (check-runner/Dockerfile) with its
// outbound internet OFF, no Worker secret and no binding, hands it the package and the source
// archive over its local port (read here from the private bucket: the container fetches
// nothing), asks it to run run.sh (C7's ClamAV scan, C8 the sandbox run, C10 the
// reproducible build) and returns the report. The container is destroyed afterwards. Used when
// RUNNER = "containers"; the CI fallback (store-check-runner.yml) runs the same run.sh.
import { DurableObject } from "cloudflare:workers";
import type { RunnerReport } from "../web/checks";
import type { Env } from "../web/env";

/** The container's port (check-runner/serve.py). */
export const RUNNER_PORT = 8080;
/** How long the container may take to answer once started (checks included). */
const DEADLINE_MS = 20 * 60_000;

export class CheckRunner extends DurableObject<Env> {
  /** Runs the checks on a submission's package and source; throws when the runner fails. */
  async run(packageKey: string, sourceKey: string): Promise<RunnerReport> {
    const c = this.ctx.container;
    if (!c) throw new Error("this Durable Object has no container (RUNNER = containers needs the containers deploy config)");
    if (!c.running) c.start({ enableInternet: false });
    const port = c.getTcpPort(RUNNER_PORT);
    const deadline = Date.now() + DEADLINE_MS;
    try {
      await ready(port, deadline);
      for (const [path, key] of [
        ["/package", packageKey],
        ["/source", sourceKey],
      ] as const) {
        const obj = await this.env.PRIVATE.get(key);
        if (!obj) throw new Error(`${key} is missing`);
        const r = await port.fetch(`http://runner${path}`, { method: "PUT", body: obj.body });
        if (!r.ok) throw new Error(`the runner refused ${path}: ${r.status}`);
      }
      const r = await port.fetch("http://runner/run", { method: "POST" });
      if (!r.ok) throw new Error(`the runner failed: ${r.status} ${(await r.text()).slice(0, 500)}`);
      return (await r.json()) as RunnerReport;
    } finally {
      await c.destroy().catch(() => undefined);
    }
  }
}

/** Waits until the container's server answers (it starts in a few seconds). */
async function ready(port: Fetcher, deadline: number): Promise<void> {
  for (;;) {
    try {
      const r = await port.fetch("http://runner/health");
      if (r.ok) return;
    } catch {
      // not listening yet
    }
    if (Date.now() > deadline) throw new Error("the runner did not start");
    await new Promise((res) => setTimeout(res, 1000));
  }
}
