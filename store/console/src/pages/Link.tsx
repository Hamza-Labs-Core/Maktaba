// /link (story 54.15 §5): the admin types the code their home shows, sees the home's name,
// version and platform, and confirms (re-auth applies). The code is never in the URL.
import { type FormEvent, useState } from "react";
import { api } from "../api";
import { useShell } from "../App";
import { useReauth } from "./Auth";

export function Link() {
  const s = useShell();
  const { run, dialog } = useReauth();
  const [code, setCode] = useState("");
  const [home, setHome] = useState<{ name: string; server_version: string; platform: string } | null>(null);
  const [done, setDone] = useState(false);
  const [error, setError] = useState("");
  if (!s.me) return <p>{s.t("link.signin")}</p>;
  if (done) return <p role="status">{s.t("link.done")}</p>;
  const find = (e: FormEvent) => {
    e.preventDefault();
    setError("");
    run(async () => setHome(await api("POST", "/v1/homes/link", { user_code: code }))).catch((err) => setError(s.errorText(err)));
  };
  const confirm = () =>
    run(async () => {
      await api("POST", "/v1/homes/link", { user_code: code, confirm: true });
      setDone(true);
    }).catch((err) => setError(s.errorText(err)));
  return (
    <section>
      <h1>{s.t("link.title")}</h1>
      {dialog}
      <form onSubmit={find}>
        <label>
          {s.t("link.code")}
          <input
            value={code}
            onChange={(e) => setCode(e.target.value.toUpperCase())}
            autoComplete="off"
            dir="ltr"
            maxLength={9}
            placeholder="XXXX-XXXX"
          />
        </label>
        <button type="submit">{s.t("link.find")}</button>
      </form>
      {home && (
        <button type="button" onClick={confirm}>
          {s.t("link.confirm", { name: home.name || "—", version: home.server_version, platform: home.platform })}
        </button>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
