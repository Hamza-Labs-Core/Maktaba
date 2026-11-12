// maktaba.tv/install/synology's page (story 59.09): the model table, the household's answers in,
// the Container Manager project's YAML out (synology.ts, all in the page: no request carries an
// answer), the five steps with DSM's screenshots once they are taken (synologyGuide.ts), after
// setup and troubleshooting. synology-main.ts mounts it with the published compose file.

import {
  DEFAULTS,
  MODELS,
  VERSION,
  limitTier,
  problems,
  synologyCompose,
  type Arch,
  type SynAnswers,
  type Tier,
} from "./synology";
import { SCREENS, screenSrc, type Step } from "./synologyGuide";
import { st, type Lang, type SynKey } from "./synologyStrings";

const OTHER: Record<string, { key: SynKey; arch: Arch; gpu: boolean; ram: number }> = {
  "other-gpu": { key: "model.otherGpu", arch: "amd64", gpu: true, ram: 8 },
  other: { key: "model.other", arch: "amd64", gpu: false, ram: 8 },
  "other-arm": { key: "model.otherArm", arch: "arm64", gpu: false, ram: 4 },
};

function modelOf(key: string): { arch: Arch; gpu: boolean; ram: number } {
  const m = MODELS.find((x) => x.name === key);
  return m ?? OTHER[key] ?? OTHER.other!;
}

function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  attrs: Record<string, string> = {},
  ...children: (Node | string)[]
): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  e.append(...children);
  return e;
}

/** A Latin name inside Arabic text keeps its own direction ("DS425+", not "+DS425"). */
const bdi = (s: string) => el("bdi", {}, s);

function field(
  lang: Lang,
  id: string,
  label: SynKey,
  control: HTMLElement,
  hint?: string
): HTMLElement {
  const wrap = el("div", { class: "field" }, el("label", { for: id }, st(lang, label)), control);
  if (hint !== undefined) {
    const h = el("p", { class: "hint", id: `${id}-hint` }, hint);
    control.setAttribute("aria-describedby", h.id);
    wrap.append(h);
  }
  return wrap;
}

function select(id: string, options: [string, string][], value: string): HTMLSelectElement {
  const s = el("select", { id, name: id });
  for (const [v, label] of options) s.append(el("option", { value: v }, label));
  s.value = value;
  return s;
}

function fitCell(lang: Lang, ram: number, maxRam: number, arch: Arch): string {
  const now = limitTier(ram, arch);
  if (now !== "storage") return st(lang, "fits", { tier: st(lang, `tier.${now}`) });
  const more = limitTier(maxRam, arch);
  if (more !== "storage")
    return st(lang, "fitsMore", { tier: st(lang, `tier.${more}`), max: maxRam });
  return st(lang, "storage");
}

function modelTable(lang: Lang): HTMLElement {
  const head = el(
    "tr",
    {},
    ...(["col.model", "col.cpu", "col.video", "col.memory", "col.fits"] as SynKey[]).map((k) =>
      el("th", { scope: "col" }, st(lang, k))
    )
  );
  const rows = MODELS.map((m) =>
    el(
      "tr",
      {},
      el("th", { scope: "row" }, bdi(m.name)),
      el("td", {}, bdi(m.cpu)),
      el("td", {}, st(lang, m.gpu ? "video.hw" : "video.sw")),
      el("td", {}, st(lang, "memory", { ram: m.ram, max: m.maxRam })),
      el("td", {}, fitCell(lang, m.ram, m.maxRam, m.arch))
    )
  );
  return el(
    "div",
    { class: "table-wrap" },
    el("table", { class: "models", id: "models" }, el("thead", {}, head), el("tbody", {}, ...rows))
  );
}

function shots(lang: Lang, step: Step): HTMLElement[] {
  return SCREENS.filter((s) => s.step === step && s.ready).map((s) =>
    el("img", {
      class: "shot",
      src: screenSrc(s.id, lang),
      alt: st(lang, `shot.${s.id}`),
      loading: "lazy",
    })
  );
}

const list = (lang: Lang, keys: SynKey[]) =>
  el("ul", {}, ...keys.map((k) => el("li", {}, st(lang, k))));

/** Builds the guide in `root` and keeps the project file in step with the answers. */
export function mount(root: HTMLElement, base: string, lang: Lang): void {
  const tzNow = Intl.DateTimeFormat().resolvedOptions().timeZone || DEFAULTS.tz;
  // U+2066/U+2069 keep "DS425+" whole in an Arabic option list
  const iso = (s: string) => `⁦${s}⁩`;
  const model = select(
    "model",
    [
      ...MODELS.map((m): [string, string] => [m.name, iso(`${m.name} (${m.cpu})`)]),
      ...Object.entries(OTHER).map(([k, o]): [string, string] => [k, st(lang, o.key)]),
    ],
    "DS425+"
  );
  const ram = el("input", {
    id: "ram",
    name: "ram",
    type: "number",
    min: "1",
    value: "6",
    dir: "ltr",
  });
  const tier = select(
    "tier",
    (["light", "standard", "strong"] as Tier[]).map((v) => [v, st(lang, `tier.${v}.limit`)]),
    DEFAULTS.tier
  );
  const uid = el("input", {
    id: "uid",
    name: "uid",
    value: DEFAULTS.uid,
    inputmode: "numeric",
    dir: "ltr",
  });
  const gpu = el("input", { id: "gpu", name: "gpu", type: "checkbox" });
  const renderGroup = el("input", {
    id: "render-group",
    name: "render-group",
    value: DEFAULTS.renderGroup,
    inputmode: "numeric",
    dir: "ltr",
  });
  const renderField = field(
    lang,
    "render-group",
    "field.renderGroup",
    renderGroup,
    st(lang, "field.renderGroup.help")
  );
  const volume = el("input", { id: "volume", name: "volume", value: DEFAULTS.volume, dir: "ltr" });
  const volumeHint = el("p", { class: "hint", id: "volume-hint" });
  volume.setAttribute("aria-describedby", volumeHint.id);
  const zone = el("input", { id: "tz", name: "tz", value: tzNow, dir: "ltr" });

  const shareList = el("div", { class: "media", id: "shares" });
  const addShare = (path: string, ro: boolean): void => {
    const row = el("div", { class: "media-row" });
    const p = el("input", {
      name: "share-path",
      value: path,
      dir: "ltr",
      "aria-label": st(lang, "field.sharePath"),
    });
    const r = el("input", { name: "share-ro", type: "checkbox" });
    r.checked = ro;
    const remove = el("button", { type: "button", class: "secondary" }, st(lang, "share.remove"));
    remove.addEventListener("click", () => {
      row.remove();
      update();
    });
    row.append(p, el("label", {}, r, " ", st(lang, "field.readOnly")), remove);
    shareList.append(row);
  };
  for (const s of DEFAULTS.shares) addShare(s.path, s.readOnly);
  const add = el("button", { type: "button", class: "secondary" }, st(lang, "share.add"));
  add.addEventListener("click", () => {
    addShare("", false);
    update();
  });

  const heading = el("h2", { id: "out-heading" });
  const note = el("p", { class: "hint", id: "version" });
  const small = el("p", { class: "problems", id: "too-small" });
  const errors = el("ul", { class: "problems", role: "alert" });
  // named by the heading above it (WCAG 4.1.2, story 60.04's axe run)
  const out = el("textarea", {
    id: "out",
    "aria-labelledby": "out-heading",
    readonly: "",
    rows: "30",
    dir: "ltr",
    spellcheck: "false",
    wrap: "off",
  });
  const copy = el("button", { type: "button" }, st(lang, "copy"));
  const download = el(
    "a",
    { download: "compose.yaml", href: "#", class: "button" },
    st(lang, "download")
  );

  const answers = (): SynAnswers => ({
    uid: uid.value,
    gpu: gpu.checked,
    renderGroup: renderGroup.value,
    tier: tier.value as Tier,
    volume: volume.value,
    shares: [...shareList.querySelectorAll<HTMLElement>(".media-row")].map((row) => ({
      path: row.querySelector<HTMLInputElement>('[name="share-path"]')!.value,
      readOnly: row.querySelector<HTMLInputElement>('[name="share-ro"]')!.checked,
    })),
    tz: zone.value,
  });

  let url = "";
  function update(): void {
    renderField.hidden = !gpu.checked;
    volumeHint.textContent = st(lang, "field.volume.help", {
      volume: volume.value.trim() || "volume1",
    });
    heading.textContent = st(lang, "result", { tier: st(lang, `tier.${tier.value as Tier}`) });
    note.textContent = st(lang, gpu.checked ? "result.j4125" : "result.plain");
    const gb = Number(ram.value);
    const tooSmall = gb > 0 && limitTier(gb, modelOf(model.value).arch) === "storage";
    small.textContent = tooSmall ? st(lang, "tooSmall", { ram: gb }) : "";
    const a = answers();
    const bad = problems(a);
    errors.replaceChildren(
      ...bad.map((p) => el("li", {}, st(lang, `problem.${p}` as SynKey))),
      ...(bad.length ? [el("li", {}, st(lang, "fixFirst"))] : [])
    );
    const ok = bad.length === 0 && !tooSmall;
    out.value = ok ? synologyCompose(base, a, a.gpu ? VERSION.j4125 : VERSION.other) : "";
    copy.toggleAttribute("disabled", !ok);
    if (url) URL.revokeObjectURL(url);
    url =
      ok && typeof URL.createObjectURL === "function"
        ? URL.createObjectURL(new Blob([out.value], { type: "text/yaml" }))
        : "";
    download.setAttribute("href", url || "#");
    download.toggleAttribute("aria-disabled", !ok);
  }

  const setRam = (gb: number): void => {
    ram.value = String(gb);
    const t = limitTier(gb, modelOf(model.value).arch);
    if (t !== "storage") tier.value = t;
  };
  model.addEventListener("change", () => {
    const m = modelOf(model.value);
    gpu.checked = m.gpu;
    setRam(Math.max(m.ram, Number(ram.value) || 0));
    update();
  });
  ram.addEventListener("input", () => {
    const t = limitTier(Number(ram.value), modelOf(model.value).arch);
    if (t !== "storage") tier.value = t;
    update();
  });
  copy.addEventListener("click", () => {
    void navigator.clipboard?.writeText(out.value).then(() => {
      copy.textContent = st(lang, "copied");
      setTimeout(() => (copy.textContent = st(lang, "copy")), 1500);
    });
  });

  const form = el(
    "form",
    { class: "answers", autocomplete: "off" },
    el("h2", {}, st(lang, "answers")),
    field(lang, "model", "field.model", model),
    field(lang, "ram", "field.ram", ram),
    field(lang, "tier", "tier.field", tier, st(lang, "tier.hint")),
    field(lang, "uid", "field.uid", uid, st(lang, "field.uid.help")),
    el("div", { class: "field" }, el("label", { class: "check" }, gpu, " ", st(lang, "field.gpu"))),
    renderField,
    el(
      "div",
      { class: "field" },
      el("label", { for: "volume" }, st(lang, "field.volume")),
      volume,
      volumeHint
    ),
    el(
      "div",
      { class: "field" },
      el("span", { class: "label" }, st(lang, "field.shares")),
      shareList,
      add
    ),
    field(lang, "tz", "field.tz", zone, st(lang, "field.tz.help"))
  );
  form.addEventListener("submit", (e) => e.preventDefault());
  form.addEventListener("input", update);
  form.addEventListener("change", update);

  const step = (n: 1 | 2 | 3 | 4 | 5) =>
    el(
      "section",
      { class: "step", id: `step${n}` },
      el("h3", {}, st(lang, `step${n}.title`)),
      el("p", {}, st(lang, `step${n}.body`)),
      ...shots(lang, n)
    );

  root.replaceChildren(
    el("p", { class: "lead" }, st(lang, "intro")),
    el("p", { class: "hint" }, st(lang, "private")),
    el(
      "section",
      { class: "before" },
      el("h2", {}, st(lang, "before")),
      modelTable(lang),
      el("p", { class: "hint" }, st(lang, "modelsNote")),
      el("p", {}, st(lang, "tiers")),
      el("h3", {}, st(lang, "prepare")),
      list(lang, ["prepare.admin", "prepare.ssd", "prepare.shares"]),
      el("p", { class: "hint" }, st(lang, "noPackage"))
    ),
    el(
      "div",
      { class: "grid" },
      form,
      el(
        "section",
        { class: "result" },
        heading,
        note,
        small,
        errors,
        out,
        el("div", { class: "actions" }, copy, download)
      )
    ),
    el(
      "section",
      { class: "steps" },
      el("h2", {}, st(lang, "steps")),
      step(1),
      step(2),
      step(3),
      step(4),
      step(5)
    ),
    el(
      "section",
      { class: "after" },
      el("h2", {}, st(lang, "after")),
      list(lang, [
        "after.firewall",
        "after.ports",
        "after.https",
        "after.video",
        "after.plugins",
        "after.inotify",
        "after.folders",
        "after.admin",
        "after.encrypted",
      ]),
      ...shots(lang, "after")
    ),
    el(
      "section",
      { class: "trouble" },
      el("h2", {}, st(lang, "trouble")),
      list(lang, ["trouble.write", "trouble.noAccess", "trouble.memory", "trouble.port"])
    )
  );
  setRam(6);
  gpu.checked = modelOf(model.value).gpu;
  update();
}
