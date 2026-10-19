// The LLM tab (design-agent.md §6): every model in the cluster with the
// machines that run it (loaded or not) and the requests in flight; the
// live request feed; and a small box to try a model through the
// OpenAI-compatible door, streamed.
import { useRef, useState, type FormEvent } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Select } from "@ds/components/Select/Select";
import { Textarea } from "@ds/components/Textarea/Textarea";
import { csrfHeaders } from "../../lib/api";
import type { ClusterStream, LiveProcess } from "../../lib/clusterStream";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { useFmt } from "../queue/format";
import { useClusterFmt } from "./format";
import { machineName, whereName } from "./names";

const BASE = (import.meta.env.VITE_API_BASE ?? "") as string;
export const CHAT_PATH = "/api/cluster/llm/v1/chat/completions";
export const FEED_SHOWN = 50;

export interface ModelPlace {
  session: string;
  machine: string;
  provider: string;
  loaded: boolean;
  maxParallel: number;
  size: number | null;
  quant: string | null;
}

export interface ClusterModel {
  name: string;
  family: string | null;
  places: ModelPlace[];
}

/** Every model the connected processes' runtimes report, merged by name. */
export function clusterModels(processes: Record<string, LiveProcess>): ClusterModel[] {
  const by = new Map<string, ClusterModel>();
  for (const p of Object.values(processes)) {
    if (!p.online) continue;
    for (const rt of p.llm?.runtimes ?? []) {
      for (const m of rt.models ?? []) {
        const entry = by.get(m.name) ?? { name: m.name, family: m.family ?? null, places: [] };
        entry.places.push({
          session: p.session,
          machine: machineName(p),
          provider: rt.provider,
          loaded: !!m.loaded,
          maxParallel: rt.max_parallel,
          size: m.size ?? null,
          quant: m.quant ?? null,
        });
        by.set(m.name, entry);
      }
    }
  }
  return [...by.values()].sort((a, b) => a.name.localeCompare(b.name));
}

/** Reads an OpenAI-style SSE body, calling `onText` with each delta. */
export async function readChatStream(
  body: ReadableStream<Uint8Array>,
  onText: (s: string) => void
): Promise<void> {
  const reader = body.getReader();
  const dec = new TextDecoder();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    let i: number;
    while ((i = buf.indexOf("\n")) >= 0) {
      const line = buf.slice(0, i).trim();
      buf = buf.slice(i + 1);
      if (!line.startsWith("data:")) continue;
      const payload = line.slice(5).trim();
      if (payload === "[DONE]") return;
      try {
        const chunk = JSON.parse(payload) as {
          choices?: { delta?: { content?: string }; text?: string }[];
        };
        const c = chunk.choices?.[0];
        const text = c?.delta?.content ?? c?.text ?? "";
        if (text) onText(text);
      } catch {
        // a keep-alive or a partial frame
      }
    }
  }
}

export function LlmPanel({ stream }: { stream: ClusterStream }) {
  const { t, n } = useI18n();
  const f = useClusterFmt();
  const models = clusterModels(stream.processes);
  const inFlight = (model: string) =>
    stream.llm.filter((r) => r.model === model && r.state === "started").length;
  const feed = stream.llm.slice(0, FEED_SHOWN);

  return (
    <div className="mkt-mc-section">
      <section className="mkt-mc-sub" aria-labelledby="mkt-cl-models-h">
        <h2 id="mkt-cl-models-h" className="mkt-mc-sub__title">
          {t("cluster.llm.models")}
        </h2>
        {models.length === 0 ? (
          <p className="mkt-mc-muted">{t("cluster.llm.noModels")}</p>
        ) : (
          <div className="mkt-mc-table-wrap">
            <table className="mkt-mc-table">
              <thead>
                <tr>
                  <th scope="col">{t("cluster.llm.model")}</th>
                  <th scope="col">{t("cluster.llm.runsOn")}</th>
                  <th scope="col">{t("cluster.llm.inFlight")}</th>
                </tr>
              </thead>
              <tbody>
                {models.map((m) => (
                  <tr key={m.name}>
                    <th scope="row" dir="ltr">
                      <code>{m.name}</code>
                    </th>
                    <td>
                      <ul className="mkt-cl-places">
                        {m.places.map((p) => {
                          const tps = stream.processes[p.session]?.metrics?.llm?.tokens_per_s;
                          return (
                            <li key={`${p.session}-${p.provider}`}>
                              {bidiIsolate(p.machine)}{" "}
                              <span className="mkt-mc-muted">
                                · {p.provider}
                                {p.quant ? ` · ${p.quant}` : ""}
                                {p.size ? ` · ${f.bytes(p.size)}` : ""}
                                {` · ${t("cluster.llm.parallel", { n: n(p.maxParallel) })}`}
                                {typeof tps === "number" &&
                                  ` · ${t("cluster.metric.tokens", { n: f.num(tps) })}`}
                              </span>{" "}
                              <Badge tone={p.loaded ? "success" : "neutral"}>
                                {t(p.loaded ? "cluster.llm.loaded" : "cluster.llm.notLoaded")}
                              </Badge>
                            </li>
                          );
                        })}
                      </ul>
                    </td>
                    <td>{n(inFlight(m.name))}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="mkt-mc-sub" aria-labelledby="mkt-cl-llmfeed-h">
        <h2 id="mkt-cl-llmfeed-h" className="mkt-mc-sub__title">
          {t("cluster.llm.feed")}
        </h2>
        {feed.length === 0 ? (
          <p className="mkt-mc-muted">{t("cluster.llm.noRequests")}</p>
        ) : (
          <ol className="mkt-cl-feed" aria-label={t("cluster.llm.feed")}>
            {feed.map((r) => (
              <li key={r.id} className={`mkt-cl-feed__row mkt-cl-feed__row--${r.state}`}>
                <time>{f.time(r.updatedAt)}</time>
                <code dir="ltr">{r.model}</code>
                <span className="mkt-mc-chip">{r.op}</span>
                <span className="mkt-mc-muted">
                  {t("cluster.llm.fromTo", {
                    from: bidiIsolate(
                      whereName(stream.processes, r.from_session) || t("cluster.llm.api")
                    ),
                    to: bidiIsolate(whereName(stream.processes, r.to_session) || "–"),
                  })}
                </span>
                <span className={`mkt-cl-outcome mkt-cl-outcome--${r.state}`}>
                  {t(`cluster.llm.state.${r.state}`)}
                </span>
                {r.usage && (
                  <span className="mkt-mc-muted">
                    {t("cluster.llm.usage", {
                      prompt: n(r.usage.prompt_tokens),
                      completion: n(r.usage.completion_tokens),
                      ms: n(r.usage.ms),
                    })}
                  </span>
                )}
                {r.error && <span className="mkt-mc-error">{r.error}</span>}
              </li>
            ))}
          </ol>
        )}
      </section>

      <TryModel models={models.map((m) => m.name)} />
    </div>
  );
}

function TryModel({ models }: { models: string[] }) {
  const { t } = useI18n();
  const fmt = useFmt();
  const [model, setModel] = useState("");
  const [prompt, setPrompt] = useState("");
  const [output, setOutput] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [took, setTook] = useState<number | null>(null);
  const abort = useRef<AbortController | null>(null);
  const chosen = model || models[0] || "";

  const send = async (e: FormEvent) => {
    e.preventDefault();
    if (!chosen || !prompt.trim()) return;
    const ctl = new AbortController();
    abort.current = ctl;
    setBusy(true);
    setOutput("");
    setError(null);
    setTook(null);
    const started = Date.now();
    try {
      const res = await fetch(`${BASE}${CHAT_PATH}`, {
        method: "POST",
        credentials: "include",
        signal: ctl.signal,
        headers: {
          "Content-Type": "application/json",
          Accept: "text/event-stream",
          ...csrfHeaders(),
        },
        body: JSON.stringify({
          model: chosen,
          stream: true,
          messages: [{ role: "user", content: prompt }],
        }),
      });
      if (!res.ok || !res.body) {
        const text = await res.text().catch(() => "");
        let detail = text.slice(0, 300);
        try {
          const p = JSON.parse(text) as {
            detail?: string;
            title?: string;
            error?: { message?: string };
          };
          detail = p.error?.message ?? p.detail ?? p.title ?? detail;
        } catch {
          // plain text
        }
        setError(t("cluster.llm.tryFailed", { detail: detail || String(res.status) }));
        return;
      }
      await readChatStream(res.body, (s) => setOutput((o) => o + s));
      setTook(Date.now() - started);
    } catch (err) {
      if ((err as { name?: string })?.name !== "AbortError")
        setError(t("cluster.llm.tryFailed", { detail: err instanceof Error ? err.message : "" }));
    } finally {
      setBusy(false);
      abort.current = null;
    }
  };

  return (
    <section className="mkt-mc-sub" aria-labelledby="mkt-cl-try-h">
      <h2 id="mkt-cl-try-h" className="mkt-mc-sub__title">
        {t("cluster.llm.try")}
      </h2>
      {models.length === 0 ? (
        <p className="mkt-mc-muted">{t("cluster.llm.noModels")}</p>
      ) : (
        <form className="mkt-cl-try" onSubmit={(e) => void send(e)}>
          <Select
            label={t("cluster.llm.model")}
            value={chosen}
            options={models.map((m) => ({ value: m, label: m }))}
            onChange={(e) => setModel(e.currentTarget.value)}
          />
          <Textarea
            label={t("cluster.llm.prompt")}
            value={prompt}
            rows={3}
            onChange={(e) => setPrompt(e.currentTarget.value)}
          />
          <div className="mkt-mc-actions">
            <Button type="submit" disabled={busy || !prompt.trim()}>
              {t("cluster.llm.send")}
            </Button>
            {busy && (
              <Button type="button" variant="secondary" onClick={() => abort.current?.abort()}>
                {t("cluster.llm.stop")}
              </Button>
            )}
          </div>
          <output
            className="mkt-cl-try__out"
            aria-live="polite"
            aria-label={t("cluster.llm.answer")}
          >
            {output}
          </output>
          {took !== null && (
            <p className="mkt-mc-muted">{t("cluster.llm.took", { time: fmt.ms(took) })}</p>
          )}
          {error && (
            <p className="mkt-mc-error" role="alert">
              {error}
            </p>
          )}
        </form>
      )}
    </section>
  );
}
