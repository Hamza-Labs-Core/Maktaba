// /install/synology and /ar/install/synology: the Synology install guide (Epic 59 story
// 59.09). The model table, the household's answers → the Container Manager project's YAML
// (install/synology.ts, built here: no answer leaves the page), the five steps with DSM's
// screenshots once they are taken (install/synologyGuide.ts), after setup, troubleshooting.
import { useEffect, useMemo, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Checkbox } from "@ds/components/Choice/Checkbox";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { useI18n } from "../shared/i18n";
import { Banner, Page } from "../shared/ui";
import {
  DEFAULTS,
  fitFor,
  MODELS,
  problems,
  renderCompose,
  type Answers,
  type Arch,
  type Fit,
  type Problem,
} from "../install/synology";
import { SCREENS, screenSrc } from "../install/synologyGuide";

const OTHER = {
  "other-gpu": { arch: "amd64" as Arch, gpu: true },
  other: { arch: "amd64" as Arch, gpu: false },
  "other-arm": { arch: "arm64" as Arch, gpu: false },
};
type OtherKey = keyof typeof OTHER;

function modelOf(key: string): { arch: Arch; gpu: boolean } {
  const m = MODELS.find((x) => x.name === key);
  return m ? { arch: m.arch, gpu: m.gpu } : (OTHER[key as OtherKey] ?? OTHER.other);
}

function Shots({ step }: { step: 1 | 2 | 3 | 4 | 5 | "after" }) {
  const { t, locale } = useI18n();
  const ready = SCREENS.filter((s) => s.step === step && s.ready);
  return (
    <>
      {ready.map((s) => (
        <img
          key={s.id}
          className="mkc-shot"
          src={screenSrc(s.id, locale)}
          alt={t(`install.syn.shot.${s.id}`)}
          loading="lazy"
        />
      ))}
    </>
  );
}

function ModelTable() {
  const { t, n } = useI18n();
  const tierName = (f: Fit) => t(`install.syn.tier.${f}`);
  return (
    <table className="mkc-table" data-testid="syn-models">
      <thead>
        <tr>
          <th>{t("install.syn.col.model")}</th>
          <th>{t("install.syn.col.cpu")}</th>
          <th>{t("install.syn.col.video")}</th>
          <th>{t("install.syn.col.memory")}</th>
          <th>{t("install.syn.col.fits")}</th>
        </tr>
      </thead>
      <tbody>
        {MODELS.map((m) => {
          const now = fitFor(m.ram, m.arch);
          const most = fitFor(m.maxRam, m.arch);
          return (
            <tr key={m.name}>
              <td>
                <bdi>{m.name}</bdi>
              </td>
              <td>
                <bdi>{m.cpu}</bdi>
              </td>
              <td>{t(m.gpu ? "install.syn.video.hw" : "install.syn.video.sw")}</td>
              <td>
                {m.maxRam > m.ram
                  ? t("install.syn.memory", { ram: n(m.ram), max: n(m.maxRam) })
                  : n(m.ram)}
              </td>
              <td>
                {now !== "storage"
                  ? t("install.syn.fits", { tier: tierName(now) })
                  : most !== "storage"
                    ? t("install.syn.fitsMore", { tier: tierName(most), max: n(m.maxRam) })
                    : t("install.syn.storage")}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

function Composer() {
  const { t } = useI18n();
  const [model, setModel] = useState("DS425+");
  const [ram, setRam] = useState("6");
  const [a, setA] = useState<Answers>({ ...DEFAULTS, gpu: true });
  const [copied, setCopied] = useState(false);
  const { arch } = modelOf(model);
  const ramGB = Number(ram);
  const fit = Number.isFinite(ramGB) && ramGB > 0 ? fitFor(ramGB, arch) : "storage";
  const answers: Answers = { ...a, tier: fit === "storage" ? "light" : fit };
  const found = problems(answers);
  const has = (p: Problem, index?: number) =>
    found.some((x) => x.problem === p && (index === undefined || x.index === index));
  const err = (p: Problem, index?: number) =>
    has(p, index) ? t(`install.syn.problem.${p}`) : undefined;
  const yaml = found.length === 0 ? renderCompose(answers, `${model}, ${ram} GB`) : "";
  const set = (patch: Partial<Answers>) => {
    setCopied(false);
    setA((x) => ({ ...x, ...patch }));
  };
  // the download is the same text, made in the page (a blob: address, nothing fetched)
  const download = useMemo(
    () =>
      yaml && typeof URL.createObjectURL === "function"
        ? URL.createObjectURL(new Blob([yaml], { type: "text/yaml" }))
        : "",
    [yaml]
  );
  useEffect(
    () => () => {
      if (download) URL.revokeObjectURL(download);
    },
    [download]
  );

  return (
    <Card>
      <form className="mkc-form" onSubmit={(e) => e.preventDefault()} aria-labelledby="syn-answers">
        <h2 id="syn-answers">{t("install.syn.answers")}</h2>
        <p className="mkc-muted">{t("install.syn.private")}</p>
        <Select
          label={t("install.syn.field.model")}
          value={model}
          onChange={(e) => {
            setModel(e.target.value);
            set({ gpu: modelOf(e.target.value).gpu });
          }}
          options={[
            ...MODELS.map((m) => ({ value: m.name, label: `\u2066${m.name} (${m.cpu})\u2069` })),
            { value: "other-gpu", label: t("install.syn.model.otherGpu") },
            { value: "other", label: t("install.syn.model.other") },
            { value: "other-arm", label: t("install.syn.model.otherArm") },
          ]}
        />
        <Input
          label={t("install.syn.field.ram")}
          type="number"
          min={1}
          max={512}
          inputMode="numeric"
          value={ram}
          onChange={(e) => setRam(e.target.value)}
        />
        <Input
          label={t("install.syn.field.uid")}
          description={t("install.syn.field.uid.help")}
          inputMode="numeric"
          dir="ltr"
          value={a.uid}
          error={err("uid")}
          onChange={(e) => set({ uid: e.target.value.trim() })}
        />
        {arch === "amd64" && (
          <Checkbox
            label={t("install.syn.field.gpu")}
            checked={a.gpu}
            onChange={(e) => set({ gpu: e.target.checked })}
          />
        )}
        {a.gpu && arch === "amd64" && (
          <Input
            label={t("install.syn.field.renderGid")}
            description={t("install.syn.field.renderGid.help")}
            inputMode="numeric"
            dir="ltr"
            value={a.renderGid}
            error={err("renderGid")}
            onChange={(e) => set({ renderGid: e.target.value.trim() })}
          />
        )}
        <Input
          label={t("install.syn.field.volume")}
          description={t("install.syn.field.volume.help", { volume: a.volume })}
          dir="ltr"
          value={a.volume}
          error={err("volume")}
          onChange={(e) => set({ volume: e.target.value.trim() })}
        />
        <fieldset className="mkc-stack">
          <legend>{t("install.syn.field.shares")}</legend>
          {a.shares.map((s, i) => (
            <div className="mkc-row mkc-guide__share" key={i} data-testid="syn-share">
              <Input
                label={t("install.syn.field.sharePath")}
                dir="ltr"
                value={s.path}
                error={err("sharePath", i) ?? err("shareTwice", i)}
                onChange={(e) =>
                  set({
                    shares: a.shares.map((x, j) => (j === i ? { ...x, path: e.target.value } : x)),
                  })
                }
              />
              <Checkbox
                label={t("install.syn.field.readOnly")}
                checked={s.readOnly}
                onChange={(e) =>
                  set({
                    shares: a.shares.map((x, j) =>
                      j === i ? { ...x, readOnly: e.target.checked } : x
                    ),
                  })
                }
              />
              <Button
                variant="ghost"
                size="sm"
                type="button"
                onClick={() => set({ shares: a.shares.filter((_, j) => j !== i) })}
              >
                {t("install.syn.share.remove")}
              </Button>
            </div>
          ))}
          {has("shares") && <Banner tone="warn">{t("install.syn.problem.shares")}</Banner>}
          <div>
            <Button
              variant="secondary"
              size="sm"
              type="button"
              onClick={() => set({ shares: [...a.shares, { path: "/volume1/", readOnly: true }] })}
            >
              {t("install.syn.share.add")}
            </Button>
          </div>
        </fieldset>
        <Input
          label={t("install.syn.field.hostIp")}
          dir="ltr"
          inputMode="decimal"
          placeholder="192.168.1.20"
          value={a.hostIp}
          error={err("hostIp")}
          onChange={(e) => set({ hostIp: e.target.value.trim() })}
        />
        <Input
          label={t("install.syn.field.tz")}
          dir="ltr"
          placeholder="Asia/Riyadh"
          value={a.tz}
          error={err("tz")}
          onChange={(e) => set({ tz: e.target.value.trim() })}
        />
        <Input
          label={t("install.syn.field.tag")}
          description={t("install.syn.field.tag.help")}
          dir="ltr"
          value={a.tag}
          error={err("tag")}
          onChange={(e) => set({ tag: e.target.value.trim() })}
        />
      </form>

      <section className="mkc-stack" aria-labelledby="syn-result">
        {fit === "storage" ? (
          <Banner tone="warn">
            <span data-testid="syn-too-small">{t("install.syn.tooSmall", { ram })}</span>
          </Banner>
        ) : (
          <>
            <h2 id="syn-result">
              {t("install.syn.result", { tier: t(`install.syn.tier.${fit}`) })}
            </h2>
            <p className="mkc-muted">
              {t(answers.gpu ? "install.syn.result.j4125" : "install.syn.result.plain")}
            </p>
            {yaml ? (
              <>
                <pre className="mkc-mono" dir="ltr" data-testid="syn-yaml" tabIndex={0}>
                  {yaml}
                </pre>
                <div className="mkc-row">
                  <Button
                    type="button"
                    onClick={() => {
                      void navigator.clipboard?.writeText(yaml).then(() => setCopied(true));
                    }}
                  >
                    {t(copied ? "install.syn.copied" : "install.syn.copy")}
                  </Button>
                  {download && (
                    <a href={download} download="compose.yaml">
                      {t("install.syn.download")}
                    </a>
                  )}
                </div>
              </>
            ) : (
              <Banner tone="info">{t("install.syn.fixFirst")}</Banner>
            )}
          </>
        )}
      </section>
    </Card>
  );
}

export function InstallSynology({ lang }: { lang?: "ar" }) {
  const { t, setLocale } = useI18n();
  useEffect(() => {
    if (lang) setLocale(lang);
  }, [lang, setLocale]);
  const steps = [1, 2, 3, 4, 5] as const;
  return (
    <Page title={t("install.syn.title")}>
      <div className="mkc-stack mkc-guide">
        <p>{t("install.syn.intro")}</p>

        <h2>{t("install.syn.before")}</h2>
        <h3>{t("install.syn.models")}</h3>
        <div className="mkc-scroll">
          <ModelTable />
        </div>
        <p className="mkc-muted">{t("install.syn.modelsNote")}</p>
        <p className="mkc-muted">{t("install.syn.tiers")}</p>
        <h3>{t("install.syn.prepare")}</h3>
        <ul>
          <li>{t("install.syn.prepare.admin")}</li>
          <li>{t("install.syn.prepare.ssd")}</li>
          <li>{t("install.syn.prepare.shares")}</li>
        </ul>
        <p className="mkc-muted">{t("install.syn.noPackage")}</p>

        <Composer />

        <h2>{t("install.syn.steps")}</h2>
        {steps.map((s) => (
          <section key={s} aria-labelledby={`syn-step-${s}`}>
            <h3 id={`syn-step-${s}`}>{t(`install.syn.step${s}.title`)}</h3>
            <p>{t(`install.syn.step${s}.body`)}</p>
            <Shots step={s} />
          </section>
        ))}

        <h2>{t("install.syn.after")}</h2>
        <ul>
          {[
            "firewall",
            "ports",
            "https",
            "video",
            "plugins",
            "inotify",
            "folders",
            "admin",
            "encrypted",
          ].map((k) => (
            <li key={k}>{t(`install.syn.after.${k}`)}</li>
          ))}
        </ul>
        <Shots step="after" />

        <h2>{t("install.syn.trouble")}</h2>
        <ul>
          {["write", "noAccess", "memory", "port"].map((k) => (
            <li key={k}>{t(`install.syn.trouble.${k}`)}</li>
          ))}
        </ul>
      </div>
    </Page>
  );
}
