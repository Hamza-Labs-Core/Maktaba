// The store's deploy config with the isolated runner on Cloudflare Containers (#756): the
// committed wrangler.jsonc plus the CheckRunner container (check-runner/Dockerfile, built
// from the repository root) and RUNNER = "containers". store-deploy.yml writes it only when
// the repository variable STORE_RUNNER is "containers" (store/README.md, Going live): until
// the owner turns Containers on for the account, a deploy with a containers block would fail,
// so the committed config keeps the CI fallback.
//
//   node scripts/containers-config.mjs [wrangler.jsonc] [out.json]
import { readFileSync, writeFileSync } from "node:fs";

/** JSONC to JSON: // and /* *\/ comments outside strings, and trailing commas, removed. */
export function stripJsonc(text) {
  let out = "";
  let inString = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (inString) {
      out += c;
      if (c === "\\") out += text[++i] ?? "";
      else if (c === '"') inString = false;
    } else if (c === '"') {
      inString = true;
      out += c;
    } else if (c === "/" && text[i + 1] === "/") {
      while (i < text.length && text[i] !== "\n") i++;
      out += "\n";
    } else if (c === "/" && text[i + 1] === "*") {
      i += 2;
      while (i < text.length && !(text[i] === "*" && text[i + 1] === "/")) i++;
      i++;
    } else {
      out += c;
    }
  }
  return out.replace(/,(\s*[}\]])/g, "$1");
}

/** The containers deploy config from the committed one. */
export function containersConfig(base) {
  const cfg = JSON.parse(stripJsonc(base));
  if (!cfg.durable_objects?.bindings?.some((b) => b.class_name === "CheckRunner")) {
    throw new Error("wrangler.jsonc binds no CheckRunner Durable Object");
  }
  cfg.vars = { ...cfg.vars, RUNNER: "containers" };
  cfg.containers = [
    {
      class_name: "CheckRunner",
      image: "./check-runner/Dockerfile",
      // the Dockerfile builds mkp from plugind/ and copies store/check-runner/
      image_build_context: "..",
      // one container per submission being checked; more wait in the queue
      max_instances: 4,
      instance_type: "standard-2",
    },
  ];
  return cfg;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const [src = "wrangler.jsonc", out = "wrangler.containers.json"] = process.argv.slice(2);
  writeFileSync(out, JSON.stringify(containersConfig(readFileSync(src, "utf8")), null, 2) + "\n");
  console.log(`wrote ${out}: RUNNER = containers, the CheckRunner container`);
}
