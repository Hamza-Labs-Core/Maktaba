// First-run setup in the desktop app (Epic 36 story 36.6 on a Mac, Epic 38
// story 38.6 on Windows and Linux; the words follow the platform).
//
// welcome (this machine or another Maktaba) → what will run + disk/RAM
// check → admin account → library folders / NAS path map → models (Hugging
// Face token for the gated Cohere model, or the NAS copy) → optional
// Plex sign-in → install (pipeline venv, the models download, finish) →
// the server starts.
//
// Every screen runs one `maktaba-server setup --json` step through the
// SetupApi (lib/desktopSetup.ts). Passwords and tokens are typed by the
// user, sent once on the step's stdin, and dropped from state after.
import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { useI18n } from "../../lib/i18n";
import {
  formatBytes,
  isStepError,
  progressPercent,
  type CheckNote,
  type Platform,
  type SetupApi,
  type StepError,
  type StepProgress,
} from "../../lib/desktopSetup";
import { ServerStarting } from "./ServerStarting";
import { platformWords } from "./platform";

export const PLEX_POLL_MS = 3000;
const HF_TOKENS_URL = "https://huggingface.co/settings/tokens";

/** The example paths of the library screens, as each OS writes them. */
const EXAMPLE_PATHS: Record<Platform, { roots: string; map: string; nas: string }> = {
  macos: {
    roots: "/Volumes/Media/Lectures",
    map: "/mnt/media => /Volumes/Media",
    nas: "/Volumes/Share/maktaba/models",
  },
  windows: {
    roots: String.raw`Y:\Lectures`,
    map: "/Volumes/Media => Y:/",
    nas: String.raw`\\NAS\Share\maktaba\models`,
  },
  linux: {
    roots: "/mnt/media/Lectures",
    map: "/Volumes/Media => /mnt/media",
    nas: "/mnt/share/maktaba/models",
  },
};

export type Screen =
  | "welcome"
  | "check"
  | "admin"
  | "libraries"
  | "models"
  | "plex"
  | "install"
  | "starting";
const ORDER: Screen[] = ["check", "admin", "libraries", "models", "plex", "install"];

interface CheckResult {
  ram_bytes: number;
  disk_free_bytes: number;
  data_dir: string;
  need: { ram_bytes: number; disk_bytes: number };
  /** no problem: Next is allowed */
  ok: boolean;
  problems: CheckNote[];
  warnings: CheckNote[];
  runs: { role: string; what: string; params?: Record<string, unknown> }[];
  /** with the second listen on: whether this machine can run it (no sign-in for its
   * gated model where it cannot; the reason is among the warnings) */
  second_listen?: { runs: boolean; code?: string; params?: Record<string, unknown> };
}

/** The platform's words ({machine}, {tray}) for every coded text. */
const WordsContext = createContext<Record<string, string>>({});

type Translate = (key: string, vars?: Record<string, string | number>) => string;

/** The text of a step's code (`${prefix}.${code}`), its params in it (sizes
 * formatted) with the platform's words; the step's English when the app has
 * no text for the code. */
export function codedText(
  t: Translate,
  prefix: string,
  code: string,
  params: Record<string, unknown> | undefined,
  words: Record<string, string>,
  english: string
): string {
  const key = `${prefix}.${code}`;
  const vars: Record<string, string | number> = { ...words };
  for (const [k, v] of Object.entries(params ?? {})) {
    if (typeof v === "number") vars[k] = k.endsWith("_bytes") ? formatBytes(v) : v;
    else if (v !== null && v !== undefined) vars[k] = String(v);
  }
  const text = t(key, vars);
  return text === key ? english : text;
}

/** What will run, in the app's words: the pipeline by its number of
 * transcription workers and the GPU it transcribes on. */
function runText(
  t: Translate,
  r: CheckResult["runs"][number],
  words: Record<string, string>
): string {
  if (r.role !== "pipeline")
    return codedText(t, "setup.check.run", r.role, r.params, words, r.what);
  const workers = Number(r.params?.workers ?? 0);
  const key = workers > 1 ? "pipelineWorkers" : workers === 1 ? "pipelineOne" : "pipeline";
  let text = codedText(t, "setup.check.run", key, r.params, words, r.what);
  if (text !== r.what && typeof r.params?.gpu === "string")
    text += codedText(t, "setup.check.run", "onGpu", r.params, words, "");
  return text;
}

function asError(e: unknown): StepError {
  if (isStepError(e)) return e;
  return { code: "internal", message: e instanceof Error ? e.message : String(e), details: [] };
}

/** One screen's frame: title, the step counter, body, error, nav. */
function Frame({
  title,
  step,
  children,
  error,
  onBack,
  next,
}: {
  title: string;
  step?: number;
  children: ReactNode;
  error?: StepError | null;
  onBack?: () => void;
  next?: ReactNode;
}) {
  const { t } = useI18n();
  return (
    <main className="mkt-setup" aria-labelledby="mkt-setup-h">
      <section className="mkt-setup__card">
        {step !== undefined && (
          <p className="mkt-setup__step">
            {t("setup.stepOf", { n: step + 1, total: ORDER.length })}
          </p>
        )}
        <h1 id="mkt-setup-h" className="mkt-setup__title">
          {title}
        </h1>
        {children}
        {error && <ErrorBox error={error} />}
        {(onBack || next) && (
          <div className="mkt-setup__actions">
            {onBack && (
              <Button type="button" variant="secondary" onClick={onBack}>
                {t("setup.back")}
              </Button>
            )}
            {next}
          </div>
        )}
      </section>
    </main>
  );
}

function ErrorBox({ error }: { error: StepError }) {
  const { t } = useI18n();
  const words = useContext(WordsContext);
  const text = codedText(t, "setup.error", error.code, error.params, words, error.message);
  // the step's English (what failed, the log's path) goes with the details
  const more = [
    ...(text !== error.message && error.message ? [error.message] : []),
    ...error.details,
  ];
  return (
    <div className="mkt-setup__error" role="alert">
      <p>{text}</p>
      {more.length > 0 && (
        <details>
          <summary>{t("setup.details")}</summary>
          <pre dir="ltr">{more.join("\n")}</pre>
        </details>
      )}
    </div>
  );
}

function Progress({ p, label }: { p: StepProgress | null; label: string }) {
  const pct = progressPercent(p);
  return (
    <div className="mkt-setup__progress">
      <ProgressBar label={label} value={pct ?? 0} indeterminate={pct === null} />
      {p && (
        <p className="mkt-setup__note" aria-live="polite">
          {p.message}
          {p.total ? ` (${formatBytes(p.done ?? 0)} / ${formatBytes(p.total)})` : ""}
        </p>
      )}
    </div>
  );
}

export function SetupWizard({ api, initial = "welcome" }: { api: SetupApi; initial?: Screen }) {
  const [screen, setScreen] = useState<Screen>(initial);
  const [serverAvailable, setServerAvailable] = useState(true);
  const [platform, setPlatform] = useState<Platform>("macos");
  useEffect(() => {
    api
      .appState()
      .then((s) => {
        setServerAvailable(s.server_available);
        if (s.platform) setPlatform(s.platform);
      })
      .catch(() => {});
  }, [api]);

  // whether this machine runs the second listen (check says): the models step
  // asks for its Hugging Face sign-in only then
  const [secondListen, setSecondListen] = useState<boolean | undefined>(undefined);
  const go = (dir: 1 | -1) => {
    const i = ORDER.indexOf(screen);
    if (dir === -1 && i <= 0) return setScreen("welcome");
    setScreen(ORDER[i + dir] ?? "starting");
  };
  const step = ORDER.indexOf(screen);
  const props = { api, step, platform, next: () => go(1), back: () => go(-1) };
  const { t } = useI18n();
  return (
    <WordsContext.Provider value={platformWords(t, platform)}>
      {screenOf(screen)}
    </WordsContext.Provider>
  );

  function screenOf(screen: Screen) {
    switch (screen) {
      case "welcome":
        return (
          <Welcome
            api={api}
            platform={platform}
            serverAvailable={serverAvailable}
            onThisMachine={() => setScreen("check")}
          />
        );
      case "check":
        return <Check {...props} onChecked={(r) => setSecondListen(r.second_listen?.runs)} />;
      case "admin":
        return <Admin {...props} />;
      case "libraries":
        return <Libraries {...props} />;
      case "models":
        return <Models {...props} secondListen={secondListen} />;
      case "plex":
        return <Plex {...props} />;
      case "install":
        return <Install {...props} />;
      case "starting":
        return <ServerStarting api={api} platform={platform} />;
    }
  }
}

interface StepProps {
  api: SetupApi;
  step: number;
  platform: Platform;
  next: () => void;
  back: () => void;
}

function Welcome({
  api,
  platform,
  serverAvailable,
  onThisMachine,
}: {
  api: SetupApi;
  platform: Platform;
  serverAvailable: boolean;
  onThisMachine: () => void;
}) {
  const { t } = useI18n();
  const words = platformWords(t, platform);
  const [url, setUrl] = useState("");
  const [error, setError] = useState<StepError | null>(null);
  const [busy, setBusy] = useState(false);
  const connect = (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    api
      .useRemoteServer(url)
      .catch((err) => setError(asError(err)))
      .finally(() => setBusy(false));
  };
  return (
    <Frame title={t("setup.welcome.title")} error={error}>
      <p>{t("setup.welcome.intro", words)}</p>
      {serverAvailable && (
        <div className="mkt-setup__choice">
          <h2>{t("setup.welcome.thisMachine", words)}</h2>
          <p className="mkt-setup__note">{t("setup.welcome.thisMachineNote", words)}</p>
          <Button type="button" onClick={onThisMachine}>
            {t("setup.welcome.thisMachineGo", words)}
          </Button>
        </div>
      )}
      <form className="mkt-setup__choice" onSubmit={connect}>
        <h2>{t("setup.welcome.remote")}</h2>
        <Input
          label={t("setup.welcome.remoteUrl")}
          dir="ltr"
          inputMode="url"
          placeholder="https://maktaba.example.com"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          required
        />
        <Button type="submit" variant="secondary" loading={busy}>
          {t("setup.welcome.remoteGo")}
        </Button>
      </form>
    </Frame>
  );
}

function Check({
  api,
  step,
  platform,
  next,
  back,
  onChecked,
}: StepProps & { onChecked?: (r: CheckResult) => void }) {
  const { t } = useI18n();
  const words = platformWords(t, platform);
  const [res, setRes] = useState<CheckResult | null>(null);
  const checked = useRef(onChecked);
  checked.current = onChecked;
  const [error, setError] = useState<StepError | null>(null);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let alive = true;
    setError(null);
    api
      .runStep<CheckResult>("check", {})
      .then((r) => {
        if (!alive) return;
        setRes(r);
        checked.current?.(r);
      })
      .catch((e) => alive && setError(asError(e)));
    return () => {
      alive = false;
    };
  }, [api, attempt]);
  return (
    <Frame
      title={t("setup.check.title")}
      step={step}
      error={error}
      onBack={back}
      next={
        error ? (
          <Button type="button" onClick={() => setAttempt((n) => n + 1)}>
            {t("common.retry")}
          </Button>
        ) : (
          <Button type="button" onClick={next} disabled={!res?.ok}>
            {t("setup.next")}
          </Button>
        )
      }
    >
      {!res && !error && <p role="status">{t("setup.check.checking", words)}</p>}
      {res && (
        <>
          <p>{t("setup.check.intro", words)}</p>
          <ul className="mkt-setup__list">
            {res.runs.map((r) => (
              <li key={r.role}>{runText(t, r, words)}</li>
            ))}
          </ul>
          <dl className="mkt-setup__facts">
            <dt>{t("setup.check.ram")}</dt>
            <dd dir="ltr">
              {t("setup.check.have", {
                have: formatBytes(res.ram_bytes),
                need: formatBytes(res.need.ram_bytes),
              })}
            </dd>
            <dt>{t("setup.check.disk")}</dt>
            <dd dir="ltr">
              {t("setup.check.have", {
                have: formatBytes(res.disk_free_bytes),
                need: formatBytes(res.need.disk_bytes),
              })}
            </dd>
            <dt>{t("setup.check.dataDir")}</dt>
            <dd dir="ltr">{res.data_dir}</dd>
          </dl>
          {res.warnings.map((n) => (
            <p key={n.code} className="mkt-setup__warn">
              {codedText(t, "setup.check.note", n.code, n.params, words, n.message)}
            </p>
          ))}
          {!res.ok && (
            <div role="alert">
              {(res.problems ?? []).map((n) => (
                <p key={n.code} className="mkt-setup__warn">
                  {codedText(t, "setup.check.note", n.code, n.params, words, n.message)}
                </p>
              ))}
              <p className="mkt-setup__warn">{t("setup.check.blocked", words)}</p>
            </div>
          )}
        </>
      )}
    </Frame>
  );
}

/** Run one step from a form; `field` errors land on the field. */
function useStep(api: SetupApi) {
  const [error, setError] = useState<StepError | null>(null);
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState<StepProgress | null>(null);
  const run = async <T,>(
    step: Parameters<SetupApi["runStep"]>[0],
    input: Record<string, unknown>
  ): Promise<T | null> => {
    setBusy(true);
    setError(null);
    setProgress(null);
    try {
      return await api.runStep<T>(step, input, setProgress);
    } catch (e) {
      setError(asError(e));
      return null;
    } finally {
      setBusy(false);
    }
  };
  const { t } = useI18n();
  const words = useContext(WordsContext);
  const fieldError = (f: string) =>
    error?.field === f
      ? codedText(t, "setup.error", error.code, error.params, words, error.message)
      : undefined;
  const formError = error && !error.field ? error : null;
  return { run, busy, progress, error, fieldError, formError };
}

function Admin({ api, step, next, back }: StepProps) {
  const { t } = useI18n();
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [mismatch, setMismatch] = useState(false);
  const s = useStep(api);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (password !== confirm) return setMismatch(true);
    setMismatch(false);
    const ok = await s.run("admin", { username, password });
    if (ok) {
      setPassword("");
      setConfirm("");
      next();
    }
  };
  return (
    <Frame title={t("setup.admin.title")} step={step} error={s.formError} onBack={back}>
      <form className="mkt-setup__form" onSubmit={submit}>
        <p>{t("setup.admin.intro")}</p>
        <Input
          label={t("login.username")}
          autoComplete="username"
          dir="ltr"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          error={s.fieldError("username")}
          required
        />
        <Input
          label={t("login.password")}
          type="password"
          autoComplete="new-password"
          dir="ltr"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          error={s.fieldError("password")}
          required
        />
        <Input
          label={t("setup.admin.confirm")}
          type="password"
          autoComplete="new-password"
          dir="ltr"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          error={mismatch ? t("setup.admin.mismatch") : undefined}
          required
        />
        <div className="mkt-setup__actions">
          <Button type="submit" loading={s.busy}>
            {t("setup.next")}
          </Button>
        </div>
      </form>
    </Frame>
  );
}

/** "from => to" lines → the path map. */
export function parsePathMap(text: string): { from: string; to: string }[] | null {
  const out: { from: string; to: string }[] = [];
  for (const raw of text.split("\n")) {
    const line = raw.trim();
    if (!line) continue;
    const [from, to, extra] = line.split("=>").map((s) => s.trim());
    if (!from || !to || extra !== undefined) return null;
    out.push({ from, to });
  }
  return out;
}

function Libraries({ api, step, platform, next, back }: StepProps) {
  const { t } = useI18n();
  const examples = EXAMPLE_PATHS[platform];
  const [roots, setRoots] = useState("");
  const [map, setMap] = useState("");
  const [mapError, setMapError] = useState(false);
  const s = useStep(api);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const pathMap = parsePathMap(map);
    if (!pathMap) return setMapError(true);
    setMapError(false);
    const list = roots
      .split("\n")
      .map((r) => r.trim())
      .filter(Boolean);
    if (await s.run("libraries", { roots: list, path_map: pathMap })) next();
  };
  return (
    <Frame title={t("setup.libraries.title")} step={step} error={s.formError} onBack={back}>
      <form className="mkt-setup__form" onSubmit={submit}>
        <p>{t("setup.libraries.intro")}</p>
        <label className="mk-field__label" htmlFor="mkt-setup-roots">
          {t("setup.libraries.roots")}
        </label>
        <textarea
          id="mkt-setup-roots"
          className="mk-control"
          dir="ltr"
          rows={3}
          placeholder={examples.roots}
          value={roots}
          onChange={(e) => setRoots(e.target.value)}
          aria-invalid={s.fieldError("roots") ? true : undefined}
          required
        />
        {s.fieldError("roots") && (
          <p className="mkt-setup__warn" role="alert">
            {s.fieldError("roots")}
          </p>
        )}
        <label className="mk-field__label" htmlFor="mkt-setup-map">
          {t("setup.libraries.map")}
        </label>
        <p className="mkt-setup__note">{t("setup.libraries.mapNote")}</p>
        <textarea
          id="mkt-setup-map"
          className="mk-control"
          dir="ltr"
          rows={2}
          placeholder={examples.map}
          value={map}
          onChange={(e) => setMap(e.target.value)}
          aria-invalid={mapError || undefined}
        />
        {mapError && (
          <p className="mkt-setup__warn" role="alert">
            {t("setup.libraries.mapBad")}
          </p>
        )}
        <div className="mkt-setup__actions">
          <Button type="submit" loading={s.busy}>
            {t("setup.next")}
          </Button>
        </div>
      </form>
    </Frame>
  );
}

function Models({
  api,
  step,
  platform,
  next,
  back,
  secondListen,
}: StepProps & { secondListen?: boolean }) {
  // false: this machine does not run the second listen, so its gated model needs no
  // Hugging Face sign-in (Whisper and the search model need none either)
  const signIn = secondListen !== false;
  const { t } = useI18n();
  const words = platformWords(t, platform);
  const [source, setSource] = useState<"huggingface" | "nas">("huggingface");
  const [token, setToken] = useState("");
  const [path, setPath] = useState("");
  const s = useStep(api);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const input = source === "huggingface" ? { source, hf_token: token } : { source, path };
    if (await s.run("models", input)) {
      setToken("");
      next();
    }
  };
  return (
    <Frame
      title={t("setup.models.title")}
      step={step}
      error={s.formError}
      onBack={s.busy ? undefined : back}
    >
      <form className="mkt-setup__form" onSubmit={submit}>
        <p>{t("setup.models.intro", words)}</p>
        <fieldset className="mkt-setup__radios" disabled={s.busy}>
          <legend className="mk-field__label">{t("setup.models.source")}</legend>
          <label>
            <input
              type="radio"
              name="source"
              checked={source === "huggingface"}
              onChange={() => setSource("huggingface")}
            />{" "}
            {t("setup.models.hf")}
          </label>
          <label>
            <input
              type="radio"
              name="source"
              checked={source === "nas"}
              onChange={() => setSource("nas")}
            />{" "}
            {t("setup.models.nas")}
          </label>
        </fieldset>
        {source === "huggingface" && !signIn ? (
          <p className="mkt-setup__note">{t("setup.models.hfNoSignIn", words)}</p>
        ) : source === "huggingface" ? (
          <>
            <p className="mkt-setup__note">
              {t("setup.models.hfNote")}{" "}
              <button
                type="button"
                className="mkt-setup__link"
                onClick={() => void api.openExternal(HF_TOKENS_URL)}
              >
                {t("setup.models.hfGetToken")}
              </button>
            </p>
            <Input
              label={t("setup.models.hfToken")}
              type="password"
              autoComplete="off"
              dir="ltr"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              error={s.fieldError("hf_token")}
              disabled={s.busy}
              required
            />
          </>
        ) : (
          <Input
            label={t("setup.models.nasPath")}
            dir="ltr"
            placeholder={EXAMPLE_PATHS[platform].nas}
            value={path}
            onChange={(e) => setPath(e.target.value)}
            error={s.fieldError("path")}
            disabled={s.busy}
            required
          />
        )}
        {s.busy && <Progress p={s.progress} label={t("setup.models.downloading")} />}
        <div className="mkt-setup__actions">
          <Button type="submit" loading={s.busy}>
            {t("setup.next")}
          </Button>
        </div>
      </form>
    </Frame>
  );
}

interface Pin {
  pin_id: number;
  code: string;
  link_url: string;
}

function Plex({ api, step, next, back }: StepProps) {
  const { t } = useI18n();
  const [pin, setPin] = useState<Pin | null>(null);
  const [pollError, setPollError] = useState<StepError | null>(null);
  const s = useStep(api);
  const nextRef = useRef(next);
  nextRef.current = next;

  useEffect(() => {
    if (!pin) return;
    // one poll at a time: the next waits PLEX_POLL_MS after this one settles,
    // so a slow plex.tv never stacks up step processes
    let alive = true;
    let timer: ReturnType<typeof setTimeout>;
    const poll = () => {
      api
        .runStep<{ linked: boolean }>("plex", { action: "poll", pin_id: pin.pin_id })
        .then((r) => {
          if (!alive) return;
          if (r.linked) nextRef.current();
          else timer = setTimeout(poll, PLEX_POLL_MS);
        })
        .catch((e) => {
          if (!alive) return;
          setPin(null);
          setPollError(asError(e));
        });
    };
    timer = setTimeout(poll, PLEX_POLL_MS);
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  }, [api, pin]);

  const start = async () => {
    setPollError(null);
    const p = await s.run<Pin>("plex", { action: "pin" });
    if (p) {
      setPin(p);
      void api.openExternal(p.link_url);
    }
  };
  const skip = async () => {
    if (await s.run("plex", { action: "skip" })) next();
  };
  return (
    <Frame
      title={t("setup.plex.title")}
      step={step}
      error={s.formError ?? pollError}
      onBack={back}
      next={
        <Button type="button" variant="secondary" onClick={() => void skip()} disabled={s.busy}>
          {t("setup.plex.skip")}
        </Button>
      }
    >
      <p>{t("setup.plex.intro")}</p>
      {pin ? (
        <div className="mkt-setup__pin" role="status">
          <p>{t("setup.plex.enterCode", { url: pin.link_url })}</p>
          <p className="mkt-setup__code" dir="ltr">
            {pin.code}
          </p>
          <p className="mkt-setup__note">{t("setup.plex.waiting")}</p>
        </div>
      ) : (
        <Button type="button" onClick={() => void start()} loading={s.busy}>
          {t("setup.plex.signIn")}
        </Button>
      )}
    </Frame>
  );
}

function Install({ api, step, platform, next, back }: StepProps) {
  const { t } = useI18n();
  const [phase, setPhase] = useState<"venv" | "models" | "llm" | "finish" | "approval" | "failed">(
    "venv"
  );
  const [progress, setProgress] = useState<StepProgress | null>(null);
  const [error, setError] = useState<StepError | null>(null);
  const [attempt, setAttempt] = useState(0);
  // The venv build runs for minutes and must run once per attempt, also
  // under StrictMode's double effect: `ran` guards the start, `mounted`
  // (not a per-effect flag) decides whether results still land.
  const ran = useRef(-1);
  const mounted = useRef(true);
  const nextRef = useRef(next);
  nextRef.current = next;
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(() => {
    if (ran.current === attempt) return;
    ran.current = attempt;
    setError(null);
    (async () => {
      try {
        setPhase("venv");
        await api.runStep("venv", {}, (p) => mounted.current && setProgress(p));
        if (!mounted.current) return;
        // the models the workers load, with the venv's own download (it resumes)
        setPhase("models");
        setProgress(null);
        await api.runStep(
          "models",
          { action: "download" },
          (p) => mounted.current && setProgress(p)
        );
        // The language model of the LLM stages (about 5 GB). Only too little disk
        // stops setup: any other failure (no network, no bundled server) leaves it
        // to the server, which downloads a missing model in the background.
        setPhase("llm");
        setProgress(null);
        try {
          await api.runStep("llm", {}, (p) => mounted.current && setProgress(p));
        } catch (e) {
          if (asError(e).code === "disk_full") throw e;
        }
        if (!mounted.current) return;
        setPhase("finish");
        setProgress(null);
        const done = await api.runStep<{ web_url: string; supervisor_url?: string }>("finish", {});
        const item = await api.finishSetup(done.web_url, done.supervisor_url);
        if (!mounted.current) return;
        if (item === "requires_approval") setPhase("approval");
        else nextRef.current();
      } catch (e) {
        if (!mounted.current) return;
        setPhase("failed");
        setError(asError(e));
      }
    })();
  }, [api, attempt]);

  if (phase === "approval") {
    // a Mac's user turns the login item on in System Settings, then comes back;
    // elsewhere the app turns its sign-in task on itself
    const mac = platform === "macos";
    const turnOn = () =>
      void api.openLoginItems().then(() => {
        if (!mac) next();
      });
    return (
      <Frame
        title={t("setup.install.approvalTitle")}
        step={step}
        next={
          <>
            <Button type="button" variant={mac ? "secondary" : "primary"} onClick={turnOn}>
              {t(`setup.install.openLoginItems.${platform}`)}
            </Button>
            {mac && (
              <Button type="button" onClick={next}>
                {t("setup.install.approved")}
              </Button>
            )}
          </>
        }
      >
        <p>{t(`setup.install.approval.${platform}`)}</p>
      </Frame>
    );
  }
  const label =
    phase === "finish"
      ? t("setup.install.finishing")
      : phase === "models"
        ? t("setup.install.models")
        : phase === "llm"
          ? t("setup.install.llm")
          : t("setup.install.venv");
  return (
    <Frame
      title={t("setup.install.title")}
      step={step}
      error={error}
      onBack={phase === "failed" ? back : undefined}
      next={
        phase === "failed" ? (
          <Button type="button" onClick={() => setAttempt((n) => n + 1)}>
            {t("common.retry")}
          </Button>
        ) : undefined
      }
    >
      <p>{label}</p>
      {phase !== "failed" && <Progress p={progress} label={label} />}
    </Frame>
  );
}
