// The plugin reference (/plugins/reference/), in English and Arabic, generated at build time
// from the files the plugin system itself reads, so it cannot drift from them:
//
//   shared/plugins/manifest.schema.json   the manifest's keys, types, defaults, limits, codes
//   shared/plugins/capabilities.toml      the capabilities, their risk and what they unlock
//   web/src/i18n/{en,ar}.json             the words an admin reads for each capability, and
//                                         the manifest, package and failure-cause messages
//   shared/plugins/events.toml            the events a stage can follow
//   shared/plugins/wit/maktaba-plugin.wit the plugin API: its interfaces, functions and errors
//   api/internal/plugins/*.go             the API's refusal codes and per-job limits
//   shared/go/componentpack/plugin.go     the package limits
//   api/internal/handlers/queues/causes.go the plugin failure causes
//
// What this file writes by hand is only what those files do not hold: the headings, a
// sentence per table, the meaning of each refusal code and the name of each limit. Each of
// those is keyed by the code or constant it explains, and the build fails (pluginFindings)
// when the source has one this file does not explain, or explains one the source no longer
// has, or when a WIT comment changed since its Arabic was written.

import fs from "node:fs";
import path from "node:path";

/** One text in both languages. */
const t = (en, ar) => ({ en, ar });

const GH = "https://github.com/Hamza-Labs-Core/Maktaba/blob/main";

// ---- the sources -------------------------------------------------------------------------

/**
 * The tiny TOML of capabilities.toml and events.toml: `schema = 1`, then `[[table]]` blocks of
 * `key = "string"`, `key = false` or `key = ["a", "b"]`. Anything else fails the build.
 * @returns {Record<string, Record<string, any>[]>}
 */
export function parseTables(text, file) {
  const out = {};
  let cur = null;
  text.split("\n").forEach((raw, i) => {
    const line = raw.replace(/\s+#.*$/, "").trim();
    if (!line || line.startsWith("#")) return;
    const head = line.match(/^\[\[([a-z_]+)\]\]$/);
    if (head) {
      cur = {};
      (out[head[1]] ??= []).push(cur);
      return;
    }
    const kv = line.match(/^([a-z_]+)\s*=\s*(.+)$/);
    if (!kv) throw new Error(`${file}:${i + 1}: not read by the plugin reference: ${raw}`);
    const value = JSON.parse(kv[2].replace(/^(true|false|\d+)$/, "$1"));
    if (cur) cur[kv[1]] = value;
  });
  return out;
}

/** Go constants `Name = <int expr>` (`8 << 20`, `10 * 60 * 1000`, `200.0`) and `Name = "word"`. */
export function goConsts(text) {
  const out = {};
  for (const m of text.matchAll(/^\s*([A-Za-z]\w*)\s*=\s*([^/\n]+?)\s*(?:\/\/.*)?$/gm)) {
    const [, name, expr] = m;
    if (/^"[^"]*"$/.test(expr)) out[name] = expr.slice(1, -1);
    else if (/^[\d.]+(\s*(<<|\*)\s*[\d.]+)*$/.test(expr)) {
      const parts = expr.split(/\s*(<<|\*)\s*/);
      let v = Number(parts[0]);
      for (let i = 1; i < parts.length; i += 2)
        v = parts[i] === "<<" ? v * 2 ** Number(parts[i + 1]) : v * Number(parts[i + 1]);
      out[name] = v;
    }
  }
  return out;
}

/** The WIT: its package, interfaces (functions with their signatures) and call-error variants. */
export function parseWit(text) {
  const pkg = text.match(/^package\s+([^;]+);/m)?.[1];
  const ifaces = [];
  let cur = null;
  let doc = [];
  const variants = [];
  let inErr = false;
  for (const raw of text.split("\n")) {
    const line = raw.trim();
    const d = line.match(/^\/\/\/\s?(.*)$/);
    if (d) {
      doc.push(d[1]);
      continue;
    }
    const iface = line.match(/^interface\s+([a-z-]+)\s*\{/);
    if (iface) {
      cur = { name: iface[1], doc: doc.join(" "), fns: [] };
      if (cur.name !== "types") ifaces.push(cur);
    }
    if (/^variant call-error\s*\{/.test(line)) inErr = true;
    else if (inErr && line === "}") inErr = false;
    else if (inErr) {
      const v = line.match(/^([a-z-]+)(\(string\))?,$/);
      if (v)
        variants.push({
          name: v[1],
          doc: doc.join(" "),
          code: doc.join(" ").match(/\(([a-z_]+)\)\s*$/)?.[1],
        });
    }
    const fn = line.match(/^([a-z-]+):\s*func(.*);$/);
    if (fn && cur && cur.name !== "types")
      cur.fns.push({ name: fn[1], sig: `${fn[1]}: func${fn[2]}`, doc: doc.join(" ") });
    if (line.startsWith("world ")) cur = null;
    doc = [];
  }
  return { pkg, ifaces, variants };
}

/** Every source, read from the repository. */
export function readPluginSources(repo) {
  const read = (f) => fs.readFileSync(path.join(repo, f), "utf8");
  const i18n = (lang) => JSON.parse(read(`web/src/i18n/${lang}.json`));
  const causes = [
    ...read("api/internal/handlers/queues/causes.go").matchAll(/^\s*"(plugin_[a-z_]+)":/gm),
  ].map((m) => m[1]);
  return {
    schema: JSON.parse(read("shared/plugins/manifest.schema.json")),
    capabilities:
      parseTables(read("shared/plugins/capabilities.toml"), "capabilities.toml").capability ?? [],
    events: parseTables(read("shared/plugins/events.toml"), "events.toml").event ?? [],
    wit: parseWit(read("shared/plugins/wit/maktaba-plugin.wit")),
    witText: read("shared/plugins/wit/maktaba-plugin.wit"),
    api: {
      ...goConsts(read("api/internal/plugins/server.go")),
      ...goConsts(read("api/internal/plugins/routes.go")),
      ...goConsts(read("api/internal/plugins/media.go")),
      ...goConsts(read("api/internal/plugins/tokens.go")),
      ...goConsts(read("api/internal/plugins/compute.go")),
    },
    pack: goConsts(read("shared/go/componentpack/plugin.go")),
    causes,
    serverGo: read("api/internal/plugins/server.go"),
    i18n: { en: i18n("en"), ar: i18n("ar") },
  };
}

// ---- what this file explains by hand -----------------------------------------------------

/**
 * Functions the API gates by a capability capabilities.toml does not list for them
 * (server.go's capabilityOf): `media.info` answers to `library.read` (54.05 §1). The build
 * fails when server.go no longer has the case.
 */
export const EXTRA_GATES = {
  "media.info": { cap: "library.read", proof: 'iface == "media" && fn == "info"' },
};

/** The plugin API's refusal codes (tokens.go, `Code*`): what each means. */
export const CODE_WORDS = {
  plugin_token: t(
    "The invocation token is missing, unknown or over: the job ended.",
    "رمز الاستدعاء مفقود أو غير معروف أو منتهٍ: انتهت المهمة."
  ),
  plugin_grants_changed: t(
    "An admin changed the plugin's grants during the job; the next job gets the new ones.",
    "غيّر مدير الصلاحيات الممنوحة للإضافة أثناء المهمة؛ وتأخذ المهمة التالية الصلاحيات الجديدة."
  ),
  plugin_capability: t(
    "The capability this call needs was not granted.",
    "الصلاحية التي يحتاجها هذا الاستدعاء غير ممنوحة."
  ),
  plugin_scope: t(
    "The subject or library is outside the job, or a library the admin did not tick.",
    "العنصر أو المكتبة خارج نطاق المهمة، أو مكتبة لم يحددها المدير."
  ),
  plugin_field: t(
    "`set-field` on a field outside the allow-list.",
    "استدعاء `set-field` على حقل خارج القائمة المسموح بها."
  ),
  plugin_quota: t("A quota or a per-job budget is used up.", "نفدت حصة أو ميزانية مخصّصة للمهمة."),
  plugin_rate: t("Too many calls in a second.", "استدعاءات كثيرة جدًا في الثانية."),
  plugin_too_large: t("A request or a value over its size limit.", "طلب أو قيمة تتجاوز حد الحجم."),
  plugin_invalid: t(
    "The arguments are not valid; the detail says why.",
    "المُعطيات غير صالحة؛ وتذكر التفاصيل السبب."
  ),
  not_found: t("No such item.", "لا يوجد عنصر كهذا."),
  media_changed: t(
    "The video's file changed since the handle was opened.",
    "تغيّر ملف المقطع منذ فُتح المقبض."
  ),
  media_unavailable: t(
    "The media is not reachable now (a network share is offline, for example).",
    "الوسائط غير متاحة الآن (كأن يكون مجلد الشبكة غير متصل)."
  ),
  plugin_not_plugin_job: t(
    "The job is not a plugin stage's job: no token is minted for it.",
    "المهمة ليست مهمة مرحلة إضافة: فلا يُصدَر لها رمز."
  ),
  plugin_not_installed: t(
    "The plugin is not installed, or not enabled.",
    "الإضافة غير مثبتة، أو غير مفعّلة."
  ),
  plugin_job_not_runnable: t(
    "The job is not claimed and running, so it gets no token.",
    "المهمة ليست محجوزة وقيد التشغيل، فلا تحصل على رمز."
  ),
  compute_unavailable: t(
    "The home's model is off or busy past the question's time (`call-error.unavailable`).",
    "نموذج الجهاز الرئيسي متوقف، أو مشغول حتى انتهاء وقت السؤال (`call-error.unavailable`)."
  ),
  compute_remote: t(
    "The language model is not on this home or the local network, so it is never asked.",
    "النموذج اللغوي ليس على هذا الجهاز الرئيسي ولا على الشبكة المحلية، فلا يُسأل أبدًا."
  ),
  compute_bad_output: t(
    "The model's answer did not follow `json-schema`, after one retry.",
    "لم تتبع إجابة النموذج `json-schema`، بعد محاولة ثانية."
  ),
};

/** The WIT's call-error variants: the Arabic of each English comment (kept with it). */
export const VARIANT_WORDS = {
  capability: t(
    "the capability was not granted (plugin_capability)",
    "الصلاحية غير ممنوحة (plugin_capability)"
  ),
  scope: t(
    "the subject or library is outside the job (plugin_scope)",
    "العنصر أو المكتبة خارج نطاق المهمة (plugin_scope)"
  ),
  quota: t("a quota or budget is used up (plugin_quota)", "نفدت حصة أو ميزانية (plugin_quota)"),
  rate: t("too many calls (plugin_rate)", "استدعاءات كثيرة جدًا (plugin_rate)"),
  "too-large": t(
    "the value is over its size limit (plugin_too_large)",
    "القيمة تتجاوز حد حجمها (plugin_too_large)"
  ),
  field: t(
    "set-field on a field outside the allow-list (plugin_field)",
    "استدعاء set-field على حقل خارج القائمة المسموح بها (plugin_field)"
  ),
  changed: t(
    "the media file changed during the job (media_changed)",
    "تغيّر ملف الوسائط أثناء المهمة (media_changed)"
  ),
  unavailable: t(
    "the media is not reachable now, e.g. the NAS is offline (media_unavailable)",
    "الوسائط غير متاحة الآن، كأن يكون جهاز التخزين الشبكي غير متصل (media_unavailable)"
  ),
  "not-found": t("no such item", "لا يوجد عنصر كهذا"),
  invalid: t(
    "the arguments are not valid; the text says why (not the plugin's words)",
    "المُعطيات غير صالحة؛ ويذكر النص السبب (وليس بكلمات الإضافة)"
  ),
  ended: t(
    "the job's token ended (job over, grants changed, plugin stopped)",
    "انتهى رمز المهمة (انتهت المهمة، أو تغيّرت الصلاحيات، أو أُوقفت الإضافة)"
  ),
};

const MB = (n) => `${n / 2 ** 20} MB`;
const KB = (n) => `${n / 2 ** 10} KB`;
const num = (n) => n.toLocaleString("en-US");

/** The limits, by the Go constant that holds each: its name and how to show its value. */
export const LIMITS = {
  api: {
    RatePerSecond: [t("Calls per second, per job", "الاستدعاءات في الثانية لكل مهمة"), num],
    RateBurst: [t("Calls in a burst, per job", "الاستدعاءات في الدفعة الواحدة لكل مهمة"), num],
    MaxBody: [t("One request's body", "حجم جسم الطلب الواحد"), MB],
    MaxValue: [
      t("One data value, suggestion or summary", "قيمة بيانات أو اقتراح أو ملخص واحد"),
      KB,
    ],
    MaxArtifact: [t("One artifact (subtitles, artwork)", "ملف ناتج واحد (ترجمة أو صورة)"), MB],
    MaxRequests: [t("`queue.request` calls, per job", "استدعاءات `queue.request` لكل مهمة"), num],
    MaxNotices: [t("`notify.admin` notices, per day", "إشعارات `notify.admin` في اليوم"), num],
    MaxRead: [t("One `media.read`", "استدعاء `media.read` واحد"), MB],
    MaxFrames: [t("Frames, per job", "الإطارات لكل مهمة"), num],
    MaxFrameEdge: [t("A frame's longest edge, in pixels", "أطول ضلع للإطار، بالبكسل"), num],
    MaxAudioMs: [
      t("One `media.audio` span, in minutes", "مدة استدعاء `media.audio` الواحد، بالدقائق"),
      (v) => num(v / 60000),
    ],
    MaxEmbedTexts: [
      t("Texts per `compute.embed`", "النصوص في استدعاء `compute.embed` الواحد"),
      num,
    ],
    MaxEmbedChars: [t("Characters per embedded text", "المحارف في النص الواحد المراد تضمينه"), num],
    MaxPrompt: [t("One `compute.generate` prompt", "موجّه `compute.generate` الواحد"), KB],
    MaxTokens: [
      t(
        "Tokens one `compute.generate` may ask for",
        "الرموز التي يطلبها استدعاء `compute.generate` الواحد"
      ),
      num,
    ],
  },
  pack: {
    PluginMaxPackedBytes: [t("A package, packed", "الحزمة مضغوطة"), MB],
    PluginMaxUnpackedBytes: [t("A package, unpacked", "الحزمة بعد فك الضغط"), MB],
    PluginMaxFiles: [t("Files in a package", "عدد الملفات في الحزمة"), num],
    PluginMaxPathBytes: [t("A path in a package, in bytes", "طول المسار في الحزمة، بالبايت"), num],
  },
};

const TYPE_AR = {
  string: "نص",
  integer: "عدد صحيح",
  number: "عدد",
  boolean: "منطقي",
  array: "قائمة",
  object: "جدول",
};

/** Problems that mean this file needs updating (empty when it explains every source). */
export function pluginFindings(src) {
  const out = [];
  const codes = Object.entries(src.api)
    .filter(([k, v]) => /^Code[A-Z]/.test(k) && typeof v === "string")
    .map(([, v]) => v);
  for (const c of codes)
    if (!CODE_WORDS[c]) out.push(`plugins.mjs: the plugin API code ${c} has no words (CODE_WORDS)`);
  for (const c of Object.keys(CODE_WORDS))
    if (!codes.includes(c))
      out.push(`plugins.mjs: CODE_WORDS explains ${c}, which tokens.go no longer has`);
  for (const v of src.wit.variants) {
    const w = VARIANT_WORDS[v.name];
    if (!w) out.push(`plugins.mjs: the WIT call-error ${v.name} has no Arabic (VARIANT_WORDS)`);
    else if (w.en !== v.doc)
      out.push(
        `plugins.mjs: the WIT comment of call-error ${v.name} changed: "${v.doc}" (VARIANT_WORDS has "${w.en}")`
      );
  }
  for (const v of Object.keys(VARIANT_WORDS))
    if (!src.wit.variants.some((x) => x.name === v))
      out.push(`plugins.mjs: VARIANT_WORDS has ${v}, which the WIT no longer has`);
  for (const [group, file] of [
    ["api", "api/internal/plugins"],
    ["pack", "shared/go/componentpack/plugin.go"],
  ])
    for (const k of Object.keys(LIMITS[group]))
      if (typeof src[group][k] !== "number")
        out.push(`plugins.mjs: ${file} has no constant ${k} (LIMITS.${group})`);
  for (const lang of ["en", "ar"]) {
    for (const c of src.capabilities)
      for (const k of [c.title_key, c.detail_key])
        if (!src.i18n[lang][k])
          out.push(`plugins.mjs: web/src/i18n/${lang}.json has no ${k} (capability ${c.name})`);
    for (const c of src.causes)
      if (!src.i18n[lang][`queue.issues.cause.${c}`])
        out.push(`plugins.mjs: web/src/i18n/${lang}.json has no queue.issues.cause.${c}`);
  }
  for (const [fn, g] of Object.entries(EXTRA_GATES))
    if (!src.serverGo.includes(g.proof))
      out.push(`plugins.mjs: server.go no longer gates ${fn} by ${g.cap} (EXTRA_GATES)`);
  if (!src.wit.pkg) out.push("plugins.mjs: the WIT names no package");
  if (!src.capabilities.length) out.push("plugins.mjs: capabilities.toml lists no capability");
  if (!src.events.length) out.push("plugins.mjs: events.toml lists no event");
  return out;
}

// ---- the page ----------------------------------------------------------------------------

// a table cell: backslashes first, so an escaped pipe can't be undone by a value's own "\"
const cell = (s) => String(s).replace(/\\/g, "\\\\").replace(/\|/g, "\\|").replace(/\n/g, " ");
const code = (s) => "`" + s + "`";
const table = (head, rows) =>
  [
    `| ${head.join(" | ")} |`,
    `|${head.map(() => "---").join("|")}|`,
    ...rows.map((r) => `| ${r.map(cell).join(" | ")} |`),
  ].join("\n");

/** The manifest's keys, depth first: [{key, spec, required}]. Keys only Maktaba's own
 * plugins may use (x-trusted-only) are left out: trustedKeys lists them. */
function manifestKeys(schema) {
  const out = [];
  const walk = (node, prefix) => {
    for (const [k, v] of Object.entries(node.properties ?? {})) {
      const key = prefix + k;
      if (v["x-trusted-only"]) continue;
      out.push({ key, spec: v, required: (node.required ?? []).includes(k) });
      if (v.properties) walk(v, key + ".");
      if (v.items?.properties) walk(v.items, key + "[].");
    }
  };
  walk(schema, "");
  return out;
}

/** The keys only Maktaba's own first-party plugins may use (Epic 54, story 54.22). */
function trustedKeys(schema) {
  const out = [];
  const walk = (node, prefix) => {
    for (const [k, v] of Object.entries(node.properties ?? {})) {
      const key = prefix + k;
      if (v["x-trusted-only"]) out.push(key);
      else if (v.properties) walk(v, key + ".");
      else if (v.items?.properties) walk(v.items, key + "[].");
    }
  };
  walk(schema, "");
  return out;
}

function limitsOf(s, lang) {
  const ar = lang === "ar";
  const parts = [];
  if (s.enum) parts.push(s.enum.map(code).join(ar ? "، " : ", "));
  if (s.items?.enum) parts.push(s.items.enum.map(code).join(ar ? "، " : ", "));
  if (s.minimum !== undefined && s.minimum === s.maximum) parts.push(num(s.minimum));
  else if (s.minimum !== undefined || s.maximum !== undefined)
    parts.push(`${s.minimum ?? "…"}–${s.maximum !== undefined ? num(s.maximum) : "…"}`);
  if (s.maxLength)
    parts.push(ar ? `${s.maxLength} محرفًا على الأكثر` : `at most ${s.maxLength} characters`);
  if (s.maxItems)
    parts.push(ar ? `${s.maxItems} عنصرًا على الأكثر` : `at most ${s.maxItems} items`);
  if (s.pattern) parts.push(ar ? "نمط في المخطط" : "a pattern, in the schema");
  return parts.join(ar ? "؛ " : "; ") || "—";
}

/** The reference page's markdown in one language. */
export function pluginReference(src, lang) {
  const ar = lang === "ar";
  const T = (x) => x[lang];
  const L = (en, a) => (ar ? a : en);
  const md = [];
  md.push(L("# Plugin reference", "# مرجع الإضافات"));
  md.push(
    L(
      "This page is generated when the site is built, from the files the plugin system itself reads: the manifest schema, the capability and event lists, the plugin API's WIT, and the API's own codes and limits. When one of them changes, this page changes with it. It describes what is on `main`.",
      "تُولَّد هذه الصفحة عند بناء الموقع من الملفات التي يقرؤها نظام الإضافات نفسه: مخطط البيان، وقائمتا الصلاحيات والأحداث، وملف WIT للواجهة البرمجية للإضافات، ورموز الواجهة البرمجية وحدودها. فإذا تغيّر أحدها تغيّرت الصفحة معه. وهي تصف ما في الفرع `main`."
    )
  );

  // the manifest
  md.push(L("## The manifest", "## البيان"));
  md.push(
    L(
      `\`manifest.toml\`, checked against [\`manifest.schema.json\`](https://maktaba.dev/plugins/manifest.schema.json) (its \`$id\` is this address; [source](${GH}/shared/plugins/manifest.schema.json)) by the same rules in Go, Rust, Python and TypeScript. An unknown key is an error. The code is what a failing key answers.`,
      `يُتحقَّق من \`manifest.toml\` بمقارنته بالمخطط [\`manifest.schema.json\`](https://maktaba.dev/plugins/manifest.schema.json) (ومعرّفه \`$id\` هو هذا العنوان؛ [المصدر](${GH}/shared/plugins/manifest.schema.json)) بالقواعد نفسها في Go وRust وPython وTypeScript. والمفتاح غير المعروف خطأ. والرمز هو ما يجيب به المفتاح المخالف.`
    )
  );
  md.push(
    table(
      L(
        ["Key", "Type", "Default", "Allowed", "Code"],
        ["المفتاح", "النوع", "الافتراضي", "المسموح", "الرمز"]
      ),
      manifestKeys(src.schema).map(({ key, spec, required }) => {
        const types = [spec.type].flat().filter(Boolean);
        return [
          code(key),
          types.map((x) => (ar ? (TYPE_AR[x] ?? x) : x)).join(ar ? " أو " : " or ") +
            (required ? L(", required", "، إلزامي") : ""),
          spec.default === undefined ? "—" : code(JSON.stringify(spec.default)),
          limitsOf(spec, lang),
          spec["x-code"] ? code(spec["x-code"]) : "—",
        ];
      })
    )
  );

  const trusted = trustedKeys(src.schema);
  if (trusted.length) {
    md.push(
      L("### Keys only Maktaba's own plugins use", "### مفاتيح لا تستخدمها إلا إضافات مكتبة نفسها")
    );
    md.push(
      L(
        `These keys, and a stage named like one of Maktaba's own, are refused in your manifest with \`plugin_trusted_field\`: only the manifests built into a Maktaba release are read with them ([Maktaba's own plugins](../first-party/)). ${trusted.map(code).join(", ")}.`,
        `تُرفض هذه المفاتيح، ومعها المرحلة التي تحمل اسم مرحلة من مراحل مكتبة، في بيانك بالرمز \`plugin_trusted_field\`: لا تُقرأ إلا في البيانات المدمجة في إصدار مكتبة ([إضافات مكتبة نفسها](../first-party/)). ${trusted.map(code).join("، ")}.`
      )
    );
  }

  // capabilities
  md.push(L("## Capabilities", "## الصلاحيات"));
  md.push(
    L(
      `From [\`capabilities.toml\`](${GH}/shared/plugins/capabilities.toml). A plugin lists the ones it needs as \`required\` or \`optional\`; the admin grants them, and the API checks the grant on every call. The words are the ones the admin reads in the app. \`plugin-data.read\` takes a plugin id: \`plugin-data.read:<id>\`.`,
      `من [\`capabilities.toml\`](${GH}/shared/plugins/capabilities.toml). تذكر الإضافة ما تحتاجه منها في \`required\` أو \`optional\`؛ ويمنحها المدير، وتتحقق الواجهة البرمجية من المنح في كل استدعاء. والكلمات هي ما يقرؤه المدير في التطبيق. وتأخذ \`plugin-data.read\` معرّف إضافة: \`plugin-data.read:<id>\`.`
    )
  );
  const RISK = { low: L("low", "منخفض"), medium: L("medium", "متوسط"), high: L("high", "مرتفع") };
  md.push(
    table(
      L(
        ["Capability", "Risk", "Unlocks", "What the admin reads", "Since"],
        ["الصلاحية", "الخطورة", "ما تتيحه", "ما يقرؤه المدير", "منذ"]
      ),
      src.capabilities.map((c) => [
        code(c.name),
        RISK[c.risk] ?? c.risk,
        code(`${c.interface}: ${c.functions.join(", ")}`),
        `**${src.i18n[lang][c.title_key]}** ${src.i18n[lang][c.detail_key]}`,
        c.since,
      ])
    )
  );

  // events
  md.push(L("## Events", "## الأحداث"));
  md.push(
    L(
      `From [\`events.toml\`](${GH}/shared/plugins/events.toml). A stage follows an event (\`[[stage.trigger]]\`), never a core stage's name: when a core stage is renamed, only this list changes. The stage's subject must be the event's.`,
      `من [\`events.toml\`](${GH}/shared/plugins/events.toml). تتبع المرحلة حدثًا (\`[[stage.trigger]]\`)، ولا تسمّي مرحلة من مراحل مكتبة نفسها أبدًا: فإذا أُعيدت تسمية مرحلة لم يتغير إلا هذه القائمة. ويجب أن يكون نوع عنصر المرحلة هو نوع عنصر الحدث.`
    )
  );
  md.push(
    table(
      L(
        ["Event", "Subject", "After the core stage", "Trigger", "Since"],
        ["الحدث", "العنصر", "بعد مرحلة مكتبة", "مُشغِّل", "منذ"]
      ),
      src.events.map((e) => [
        code(e.name),
        code(e.subject),
        e.stage ? code(e.stage) : "—",
        e.trigger === false
          ? L("no: `[[schedule]]` only", "لا: `[[schedule]]` فقط")
          : L("yes", "نعم"),
        e.since,
      ])
    )
  );

  // the API
  md.push(L("## The plugin API", "## الواجهة البرمجية للإضافات"));
  md.push(
    L(
      `\`${src.wit.pkg}\`, from [\`maktaba-plugin.wit\`](${GH}/shared/plugins/wit/maktaba-plugin.wit). A plugin is one WebAssembly component of the world \`plugin\`, which exports \`run(stage)\` and imports these interfaces. An interface whose capability was not granted is still linked; each call answers \`call-error.capability\`.`,
      `\`${src.wit.pkg}\`، من [\`maktaba-plugin.wit\`](${GH}/shared/plugins/wit/maktaba-plugin.wit). الإضافة مكوّن WebAssembly واحد من العالم \`plugin\`، يصدّر \`run(stage)\` ويستورد هذه الواجهات. والواجهة التي لم تُمنح صلاحيتها تبقى مربوطة، لكن كل استدعاء لها يجيب بـ\`call-error.capability\`.`
    )
  );
  const capsFor = (iface, fn) =>
    EXTRA_GATES[`${iface}.${fn}`]
      ? [code(EXTRA_GATES[`${iface}.${fn}`].cap)]
      : src.capabilities
          .filter(
            (c) => c.interface === iface && (c.functions.includes("*") || c.functions.includes(fn))
          )
          .map((c) => code(c.name));
  md.push(
    table(
      L(["Interface", "Function", "Capability"], ["الواجهة", "الدالة", "الصلاحية"]),
      src.wit.ifaces.flatMap((i) =>
        i.fns.map((f) => {
          const caps = capsFor(i.name, f.name);
          return [
            code(i.name),
            code(f.sig),
            caps.length ? caps.join(ar ? " أو " : " or ") : L("none", "لا شيء"),
          ];
        })
      )
    )
  );
  md.push(L("### Call errors", "### أخطاء الاستدعاء"));
  md.push(
    L(
      "Every function that can be refused answers a `call-error`. Each variant is one of the API's refusal codes:",
      "كل دالة يمكن رفضها تجيب بـ`call-error`. وكل خيار منه رمز من رموز رفض الواجهة البرمجية:"
    )
  );
  md.push(
    table(
      L(["Variant", "Means"], ["الخيار", "معناه"]),
      src.wit.variants.map((v) => [code(v.name), VARIANT_WORDS[v.name]?.[lang] ?? v.doc])
    )
  );
  md.push(L("### Refusal codes", "### رموز الرفض"));
  md.push(
    L(
      `What the plugin API answers the host (\`api/internal/plugins\`); the host turns each into a \`call-error\`. Every refusal is logged and counted for the plugin.`,
      `ما تجيب به الواجهة البرمجية للإضافات مضيفَ الإضافات (\`api/internal/plugins\`)؛ ويحوّل المضيف كلًّا منها إلى \`call-error\`. ويُسجَّل كل رفض ويُحسب على الإضافة.`
    )
  );
  const codes = Object.entries(src.api)
    .filter(([k, v]) => /^Code[A-Z]/.test(k) && typeof v === "string")
    .map(([, v]) => v);
  md.push(
    table(
      L(["Code", "Means"], ["الرمز", "معناه"]),
      codes.map((c) => [code(c), CODE_WORDS[c]?.[lang] ?? "—"])
    )
  );

  md.push(L("### Limits", "### الحدود"));
  md.push(
    L(
      "The API's own limits, from its constants. The manifest's `[resources]` (above) sets the rest: memory, time, fuel, scratch, data and concurrency.",
      "حدود الواجهة البرمجية نفسها، من ثوابتها. ويحدد قسم `[resources]` في البيان (أعلاه) الباقي: الذاكرة والوقت والوقود ومساحة العمل المؤقتة والبيانات والتوازي."
    )
  );
  md.push(
    table(
      L(["Limit", "Value"], ["الحد", "القيمة"]),
      Object.entries(LIMITS.api).map(([k, [label, fmt]]) => [T(label), fmt(src.api[k])])
    )
  );

  // the package
  md.push(L("## The package", "## الحزمة"));
  md.push(
    L(
      `A \`.mkplugin\` file is a tar archive compressed with zstd, checked on unpacking by [\`componentpack\`](${GH}/shared/go/componentpack/plugin.go): no absolute paths, \`..\`, links or devices, and every file listed with its size and SHA-256 in \`files.json\`.`,
      `ملف \`.mkplugin\` أرشيف tar مضغوط بـzstd، يُفحص عند فك ضغطه بواسطة [\`componentpack\`](${GH}/shared/go/componentpack/plugin.go): لا مسارات مطلقة ولا \`..\` ولا روابط ولا أجهزة، وكل ملف مذكور بحجمه وبصمة SHA-256 الخاصة به في \`files.json\`.`
    )
  );
  md.push(
    table(
      L(["Limit", "Value"], ["الحد", "القيمة"]),
      Object.entries(LIMITS.pack).map(([k, [label, fmt]]) => [T(label), fmt(src.pack[k])])
    )
  );

  // errors an admin sees
  md.push(L("## Manifest and package errors", "## أخطاء البيان والحزمة"));
  md.push(
    L(
      "The words the admin reads when a plugin is refused, from the app's own strings (`plugins.error.<code>`).",
      "الكلمات التي يقرؤها المدير حين تُرفض إضافة، من نصوص التطبيق نفسه (`plugins.error.<code>`)."
    )
  );
  const errs = Object.keys(src.i18n.en).filter((k) => k.startsWith("plugins.error."));
  md.push(
    table(
      L(["Code", "The admin reads"], ["الرمز", "ما يقرؤه المدير"]),
      errs.map((k) => [code(k.slice("plugins.error.".length)), src.i18n[lang][k] ?? src.i18n.en[k]])
    )
  );

  md.push(L("## Failure causes", "## أسباب الإخفاق"));
  md.push(
    L(
      "How a plugin job that failed is filed in the queue, with the words the queue shows (`queue.issues.cause.<cause>`).",
      "كيف تُصنَّف مهمة الإضافة التي أخفقت في الطابور، مع الكلمات التي يعرضها الطابور (`queue.issues.cause.<cause>`)."
    )
  );
  md.push(
    table(
      L(["Cause", "The queue shows"], ["السبب", "ما يعرضه الطابور"]),
      src.causes.map((c) => [code(c), src.i18n[lang][`queue.issues.cause.${c}`]])
    )
  );

  md.push(L("## The WIT", "## ملف WIT"));
  md.push(L("The whole interface, as the host links it:", "الواجهة كاملةً كما يربطها المضيف:"));
  md.push("```wit\n" + src.witText.trimEnd() + "\n```");
  return md.join("\n\n") + "\n";
}
