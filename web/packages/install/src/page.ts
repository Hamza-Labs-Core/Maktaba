// The install page's form: the household's answers in, the filled compose file out, all in
// the page (no request carries an answer). main.ts mounts it with the published file.

import { fillCompose, problems, tierFor, type Answers, type Tier, type Video } from "./compose";
import { t, type Key, type Lang } from "./strings";

export type Nas = "qnap" | "ugreen" | "asustor" | "linux";

/** Where each NAS keeps a container's folder (the data folder's default). */
export const DATA_DEFAULT: Record<Nas, string> = {
  qnap: "/share/Container/maktaba",
  ugreen: "/volume1/docker/maktaba",
  asustor: "/volume1/Docker/maktaba",
  linux: "",
};

/** A media folder's example path on each NAS. */
export const MEDIA_EXAMPLE: Record<Nas, string> = {
  qnap: "/share/Multimedia",
  ugreen: "/volume1/Media",
  asustor: "/volume1/Media",
  linux: "/srv/media",
};

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

function field(lang: Lang, id: string, label: Key, control: HTMLElement, hint?: Key): HTMLElement {
  const wrap = el("div", { class: "field" }, el("label", { for: id }, t(lang, label)), control);
  if (hint) {
    const h = el("p", { class: "hint", id: `${id}-hint` }, t(lang, hint));
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

/** Builds the form in `root` and keeps the output in step with it. */
export function mount(root: HTMLElement, base: string, lang: Lang): void {
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || "Etc/UTC";
  const nas = select(
    "nas",
    (["qnap", "ugreen", "asustor", "linux"] as Nas[]).map((n) => [n, t(lang, `nas.${n}` as Key)]),
    "ugreen"
  );
  const nasHint = el("p", { class: "hint", id: "nas-hint" });
  const user = el("input", {
    id: "user",
    name: "user",
    value: "1000:100",
    inputmode: "numeric",
    dir: "ltr",
  });
  const ram = el("input", {
    id: "ram",
    name: "ram",
    type: "number",
    min: "1",
    value: "8",
    dir: "ltr",
  });
  const tier = select(
    "tier",
    (["light", "standard", "strong", "none"] as Tier[]).map((v) => [
      v,
      t(lang, `tier.${v}` as Key),
    ]),
    "standard"
  );
  const data = el("input", { id: "data", name: "data", value: DATA_DEFAULT.ugreen, dir: "ltr" });
  const mediaList = el("div", { class: "media", id: "media" });
  const addMedia = (path: string, ro: boolean): void => {
    const row = el("div", { class: "media-row" });
    const p = el("input", {
      name: "media-path",
      value: path,
      dir: "ltr",
      "aria-label": t(lang, "media"),
    });
    const r = el("input", { name: "media-ro", type: "checkbox" });
    r.checked = ro;
    row.append(p, el("label", {}, r, " ", t(lang, "readOnly")));
    mediaList.append(row);
  };
  addMedia(MEDIA_EXAMPLE.ugreen, true);
  const add = el("button", { type: "button", class: "secondary" }, t(lang, "addFolder"));
  add.addEventListener("click", () => {
    addMedia("", false);
    update();
  });
  const video = select(
    "video",
    (["none", "intel-amd", "nvidia"] as Video[]).map((v) => [v, t(lang, `video.${v}` as Key)]),
    "none"
  );
  const renderGroup = el("input", {
    id: "render-group",
    name: "render-group",
    value: "",
    dir: "ltr",
  });
  const renderField = field(lang, "render-group", "renderGroup", renderGroup, "renderGroup.hint");
  const zone = el("input", { id: "tz", name: "tz", value: tz, dir: "ltr" });
  const network = select(
    "network",
    [
      ["host", t(lang, "network.host")],
      ["bridge", t(lang, "network.bridge")],
    ],
    "host"
  );
  const hostIp = el("input", { id: "host-ip", name: "host-ip", value: "", dir: "ltr" });
  const ipField = field(lang, "host-ip", "hostIp", hostIp, "hostIp.hint");
  const out = el("textarea", {
    id: "out",
    readonly: "",
    rows: "30",
    dir: "ltr",
    spellcheck: "false",
  });
  const errors = el("ul", { class: "problems", role: "alert" });
  const copy = el("button", { type: "button" }, t(lang, "copy"));
  const download = el(
    "a",
    { download: "docker-compose.yml", href: "#", class: "button" },
    t(lang, "download")
  );

  const answers = (): Answers => ({
    user: user.value,
    data: data.value,
    media: [...mediaList.querySelectorAll<HTMLElement>(".media-row")].map((row) => ({
      path: row.querySelector<HTMLInputElement>('[name="media-path"]')!.value,
      readOnly: row.querySelector<HTMLInputElement>('[name="media-ro"]')!.checked,
    })),
    tier: tier.value as Tier,
    video: video.value as Video,
    renderGroup: renderGroup.value,
    tz: zone.value,
    network: network.value as "host" | "bridge",
    hostIp: hostIp.value,
  });

  let url = "";
  function update(): void {
    nasHint.textContent = t(lang, `nas.hint.${nas.value}` as Key);
    renderField.hidden = video.value !== "intel-amd";
    ipField.hidden = network.value !== "bridge";
    const a = answers();
    const bad = problems(a);
    errors.replaceChildren(...bad.map((p) => el("li", {}, t(lang, `problem.${p}` as Key))));
    const ok = bad.length === 0;
    out.value = ok ? fillCompose(base, a) : "";
    copy.toggleAttribute("disabled", !ok);
    if (url) URL.revokeObjectURL(url);
    url =
      ok && typeof URL.createObjectURL === "function"
        ? URL.createObjectURL(new Blob([out.value], { type: "text/yaml" }))
        : "";
    download.setAttribute("href", url || "#");
    download.toggleAttribute("aria-disabled", !ok);
  }

  nas.addEventListener("change", () => {
    data.value = DATA_DEFAULT[nas.value as Nas];
    const first = mediaList.querySelector<HTMLInputElement>('[name="media-path"]');
    if (first) first.value = MEDIA_EXAMPLE[nas.value as Nas];
    update();
  });
  ram.addEventListener("input", () => {
    tier.value = tierFor(Number(ram.value));
    update();
  });
  copy.addEventListener("click", () => {
    void navigator.clipboard?.writeText(out.value).then(() => {
      copy.textContent = t(lang, "copied");
      setTimeout(() => (copy.textContent = t(lang, "copy")), 1500);
    });
  });
  root.addEventListener("input", update);
  root.addEventListener("change", update);

  const form = el(
    "form",
    { class: "answers", autocomplete: "off" },
    field(lang, "nas", "nas", nas),
    nasHint,
    field(lang, "user", "user", user, "user.hint"),
    field(lang, "ram", "ram", ram),
    field(lang, "tier", "tier", tier, "tier.hint"),
    field(lang, "data", "data", data, "data.hint"),
    el(
      "div",
      { class: "field" },
      el("span", { class: "label" }, t(lang, "media")),
      mediaList,
      add,
      el("p", { class: "hint" }, t(lang, "media.hint"))
    ),
    field(lang, "video", "video", video),
    renderField,
    field(lang, "tz", "tz", zone, "tz.hint"),
    field(lang, "network", "network", network),
    ipField
  );
  form.addEventListener("submit", (e) => e.preventDefault());
  root.replaceChildren(
    form,
    el(
      "section",
      { class: "result" },
      el("h2", {}, t(lang, "result")),
      errors,
      out,
      el("div", { class: "actions" }, copy, download),
      el("p", {}, t(lang, "next"))
    )
  );
  update();
}
