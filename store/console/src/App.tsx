// The console's shell: language (English / Arabic, RTL), theme (light / dark), the signed-in
// account, the navigation and the page for the path.
import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { ApiError, api } from "./api";
import { type Key, type Lang, translate } from "./i18n";
import { Account } from "./pages/Account";
import { Developers } from "./pages/Developers";
import { Link } from "./pages/Link";
import { SignIn, SignUp, Verify } from "./pages/Auth";

export interface Me {
  id: string;
  email: string;
  email_verified: boolean;
  display_name: string;
  locale: Lang;
  roles: string[];
  factors: { id: string; kind: "totp" | "passkey"; name: string; added: string; last_used: string | null }[];
  sessions: { id: string; created: string; last_seen: string; device: string; current: boolean }[];
}

interface Shell {
  lang: Lang;
  t: (k: Key | string, p?: Record<string, string | number>) => string;
  me: Me | null;
  reload: () => Promise<void>;
  go: (path: string) => void;
  errorText: (e: unknown) => string;
}

const Ctx = createContext<Shell | null>(null);
export const useShell = () => useContext(Ctx)!;

function initialLang(): Lang {
  try {
    const s = localStorage.getItem("mks:lang");
    if (s === "ar" || s === "en") return s;
  } catch {
    /* no storage */
  }
  return navigator.language?.startsWith("ar") ? "ar" : "en";
}

export function App({ path: initialPath }: { path?: string }) {
  const [lang, setLang] = useState<Lang>(initialLang);
  const [theme, setTheme] = useState<"light" | "dark" | "">("");
  const [path, setPath] = useState(initialPath ?? location.pathname);
  const [me, setMe] = useState<Me | null>(null);
  const [loaded, setLoaded] = useState(false);

  const reload = useCallback(async () => {
    try {
      setMe(await api<Me>("GET", "/v1/me"));
    } catch {
      setMe(null);
    }
    setLoaded(true);
  }, []);
  useEffect(() => {
    void reload();
  }, [reload]);
  useEffect(() => {
    document.documentElement.lang = lang;
    document.documentElement.dir = lang === "ar" ? "rtl" : "ltr";
    try {
      localStorage.setItem("mks:lang", lang);
    } catch {
      /* no storage */
    }
  }, [lang]);
  useEffect(() => {
    if (theme) document.documentElement.dataset.theme = theme;
    else delete document.documentElement.dataset.theme;
  }, [theme]);
  useEffect(() => {
    const pop = () => setPath(location.pathname);
    addEventListener("popstate", pop);
    return () => removeEventListener("popstate", pop);
  }, []);

  const shell = useMemo<Shell>(
    () => ({
      lang,
      t: (k, p) => translate(lang, k, p),
      me,
      reload,
      go: (p) => {
        history.pushState(null, "", p);
        setPath(p);
      },
      errorText: (e) => (e instanceof ApiError ? translate(lang, `error.${e.code}`) : translate(lang, "error.generic")),
    }),
    [lang, me, reload],
  );

  const page = (() => {
    if (path.startsWith("/verify")) return <Verify />;
    if (path.startsWith("/signup")) return <SignUp />;
    if (path.startsWith("/link")) return <Link />;
    if (!me) return <SignIn />;
    if (path.startsWith("/developers")) return <Developers />;
    return <Account />;
  })();

  const nav = (p: string, k: Key) => (
    <a
      href={p}
      aria-current={path.startsWith(p) ? "page" : undefined}
      onClick={(e) => {
        e.preventDefault();
        shell.go(p);
      }}
    >
      {shell.t(k)}
    </a>
  );

  return (
    <Ctx.Provider value={shell}>
      <header className="bar">
        <strong>{shell.t("app.name")}</strong>
        <nav aria-label={shell.t("app.name")}>
          {me ? (
            <>
              {nav("/account", "nav.account")}
              {nav("/developers", "nav.developers")}
              {nav("/link", "nav.link")}
            </>
          ) : (
            <>
              {nav("/signin", "nav.signin")}
              {nav("/signup", "nav.signup")}
            </>
          )}
        </nav>
        <span className="spacer" />
        <button type="button" onClick={() => setLang(lang === "en" ? "ar" : "en")} lang={lang === "en" ? "ar" : "en"}>
          {shell.t("lang.switch")}
        </button>
        <button type="button" aria-label={shell.t("theme.toggle")} onClick={() => setTheme(theme === "dark" ? "light" : "dark")}>
          ◐
        </button>
      </header>
      <main>{loaded ? page : null}</main>
    </Ctx.Provider>
  );
}
