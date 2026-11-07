// The developer console: the agreement, signing keys, id prefixes, plugins and submissions.
import { useEffect, useState } from "react";
import { api } from "../api";
import { useShell } from "../App";
import { useReauth } from "./Auth";

interface DevData {
  developer: { agreement_version: number; status: string } | null;
  agreement_current: number;
  keys: { id: string; added: string; revoked_at: string | null; suspended_at: string | null }[];
  prefixes: { prefix: string; kind: string; token: string; verified_at: string | null; lapsed_at: string | null }[];
  plugins: { id: string; names: string; status: string }[];
  submissions: { id: string; plugin: string; version: string; status: string; created: string }[];
}

export function Developers() {
  const s = useShell();
  const me = s.me!;
  const { run, dialog } = useReauth();
  const [d, setD] = useState<DevData | null>(null);
  const [keyText, setKeyText] = useState("");
  const [prefix, setPrefix] = useState("");
  const [handle, setHandle] = useState("");
  const [error, setError] = useState("");
  const load = () => api<DevData>("GET", "/v1/developers/me").then(setD, (e) => setError(s.errorText(e)));
  useEffect(() => {
    void load();
  }, []);
  const act = (f: () => Promise<unknown>) => () =>
    run(async () => (await f(), await load(), await s.reload())).catch((e) => setError(s.errorText(e)));

  if (!d) return error ? <p role="alert">{error}</p> : null;
  const isDev = me.roles.includes("developer");
  const current = d.developer?.agreement_version === d.agreement_current;
  return (
    <section>
      <h1>{s.t("dev.title")}</h1>
      {error && <p role="alert">{error}</p>}
      {dialog}
      {(!isDev || !current) && (
        <div>
          {isDev && <p className="warn">{s.t("dev.agreement.new")}</p>}
          <p>{s.t("dev.agreement", { version: d.agreement_current })}</p>
          <button type="button" onClick={act(() => api("POST", "/v1/developers", { agreement_version: d.agreement_current }))}>
            {s.t("dev.become")}
          </button>
        </div>
      )}
      {isDev && me.factors.length === 0 && <p className="warn">{s.t("dev.mfa")}</p>}
      {isDev && (
        <>
          <h2>{s.t("dev.keys")}</h2>
          <ul>
            {d.keys.map((k) => (
              <li key={k.id}>
                <code dir="ltr">{k.id}</code>{" "}
                {k.revoked_at ? (
                  s.t("dev.keys.revoked")
                ) : k.suspended_at ? (
                  s.t("dev.keys.suspended")
                ) : (
                  <button type="button" onClick={act(() => api("DELETE", `/v1/developers/keys/${k.id}`))}>
                    {s.t("dev.keys.revoke")}
                  </button>
                )}
              </li>
            ))}
          </ul>
          <label>
            {s.t("dev.keys.help")}
            <textarea dir="ltr" value={keyText} onChange={(e) => setKeyText(e.target.value)} rows={2} />
          </label>
          <button
            type="button"
            onClick={act(async () => {
              await api("POST", "/v1/developers/keys", JSON.parse(keyText));
              setKeyText("");
            })}
          >
            {s.t("dev.keys.add")}
          </button>

          <h2>{s.t("dev.prefixes")}</h2>
          <ul>
            {d.prefixes.map((p) => (
              <li key={p.prefix}>
                <code dir="ltr">{p.prefix}</code>{" "}
                {p.lapsed_at
                  ? s.t("dev.prefix.lapsed")
                  : p.verified_at
                    ? s.t("dev.prefix.verified")
                    : s.t("dev.prefix.txt", { domain: p.prefix.split(".").reverse().join("."), txt: `maktaba-developer=${p.token}` })}
                {p.kind === "dns" && (
                  <button type="button" onClick={act(() => api("POST", `/v1/developers/prefixes/${p.prefix}/check`))}>
                    {s.t("dev.prefix.check")}
                  </button>
                )}
              </li>
            ))}
          </ul>
          <label>
            {s.t("dev.prefix.domain")}
            <input dir="ltr" value={prefix} onChange={(e) => setPrefix(e.target.value)} />
          </label>
          <button type="button" onClick={act(() => api("POST", "/v1/developers/prefixes", { prefix }))}>
            {s.t("dev.prefix.claim")}
          </button>
          <label>
            {s.t("dev.prefix.community")}
            <input dir="ltr" value={handle} onChange={(e) => setHandle(e.target.value)} />
          </label>
          <button type="button" onClick={act(() => api("POST", "/v1/developers/prefixes", { community: handle }))}>
            {s.t("dev.prefix.claim")}
          </button>

          <h2>{s.t("dev.plugins")}</h2>
          <ul>
            {d.plugins.map((p) => (
              <li key={p.id}>
                <code dir="ltr">{p.id}</code> · {s.t(`status.${p.status}`)}{" "}
                {(p.status === "listed" || p.status === "draft") && (
                  <button type="button" onClick={act(() => api("POST", `/v1/plugins/${p.id}/withdraw`))}>
                    {s.t("dev.withdraw")}
                  </button>
                )}
              </li>
            ))}
          </ul>
          <h2>{s.t("dev.submissions")}</h2>
          <ul>
            {d.submissions.map((x) => (
              <li key={x.id}>
                <code dir="ltr">
                  {x.plugin} {x.version}
                </code>{" "}
                · {s.t(`status.${x.status}`)}
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}
