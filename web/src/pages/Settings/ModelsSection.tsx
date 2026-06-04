// Settings → Models. Model Management surface backed by the
// /api/models endpoints (api/internal/handlers/models). Models are
// grouped by type into cards; each card exposes download (with live
// progress), delete, activate and test. An active-backend selector and
// the OpenAI API key field sit above the groups.
//
// State lifecycle: download returns 202 and then advances progress
// server-side, so we poll `list()` on a short interval but ONLY while
// something is downloading — the timer disarms itself once everything
// settles.
import { useCallback, useEffect, useRef, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { Select } from "@ds/components/Select/Select";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";

type ModelType = "stt" | "embedding" | "diarization";
type Status = "active" | "downloaded" | "downloading" | "available";

interface TestResult {
  ok: boolean;
  latency_ms: number;
  detail?: string;
}

interface Model {
  id: string;
  type: ModelType;
  name: string;
  size: string;
  platform: string;
  status: Status;
  progress: number;
  active: boolean;
  last_test?: TestResult;
}

const GROUPS: ModelType[] = ["stt", "embedding", "diarization"];

const STATUS_TONE: Record<Status, "neutral" | "accent" | "success" | "warn"> = {
  active: "success",
  downloaded: "accent",
  downloading: "warn",
  available: "neutral",
};

const API_KEY_STORAGE = "mkt:openai_api_key";

export function ModelsSection() {
  const { t } = useI18n();
  const [models, setModels] = useState<Model[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [apiKey, setApiKey] = useState(() => {
    try {
      return localStorage.getItem(API_KEY_STORAGE) ?? "";
    } catch {
      return "";
    }
  });
  const pollRef = useRef<number | null>(null);

  const load = useCallback(async () => {
    try {
      const data = await api.get<Model[]>("/api/models");
      setModels(data);
      setError(null);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("settings.models.loadError"));
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  // Arm a poll only while something is downloading; disarm otherwise.
  const downloading = models.some((m) => m.status === "downloading");
  useEffect(() => {
    if (downloading && pollRef.current === null) {
      pollRef.current = window.setInterval(() => void load(), 600);
    } else if (!downloading && pollRef.current !== null) {
      window.clearInterval(pollRef.current);
      pollRef.current = null;
    }
    return () => {
      if (pollRef.current !== null) {
        window.clearInterval(pollRef.current);
        pollRef.current = null;
      }
    };
  }, [downloading, load]);

  async function act(fn: () => Promise<unknown>) {
    try {
      await fn();
      await load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("settings.models.loadError"));
    }
  }

  const download = (id: string) => act(() => api.post(`/api/models/${id}/download`));
  const remove = (id: string) => act(() => api.delete(`/api/models/${id}`));
  const activate = (id: string) => act(() => api.patch(`/api/models/${id}/activate`));
  const test = (id: string) => act(() => api.post(`/api/models/${id}/test`));

  function saveApiKey() {
    try {
      localStorage.setItem(API_KEY_STORAGE, apiKey);
    } catch {
      /* ignored */
    }
  }

  // STT backend selector reflects the active STT model.
  const sttModels = models.filter((m) => m.type === "stt");
  const activeStt = sttModels.find((m) => m.active)?.id ?? "";
  const downloadedStt = sttModels.filter((m) => m.status === "downloaded" || m.status === "active");

  return (
    <div className="mkt-settings__panel mkt-models">
      <h2>{t("settings.models.title")}</h2>

      {error && (
        <p className="mkt-models__error" role="alert">
          {error}
        </p>
      )}

      <div className="mkt-models__controls">
        <Select
          label={t("settings.models.activeBackend")}
          value={activeStt}
          onChange={(e) => {
            const id = e.currentTarget.value;
            if (id) void activate(id);
          }}
          options={downloadedStt.map((m) => ({ value: m.id, label: m.name }))}
        />

        <div className="mkt-models__apikey">
          <Input
            type="password"
            label={t("settings.models.apiKey")}
            placeholder={t("settings.models.apiKeyPlaceholder")}
            value={apiKey}
            onChange={(e) => setApiKey(e.currentTarget.value)}
            autoComplete="off"
          />
          <Button variant="secondary" size="sm" onClick={saveApiKey}>
            {t("settings.models.apiKeySave")}
          </Button>
        </div>
      </div>

      {GROUPS.map((group) => {
        const inGroup = models.filter((m) => m.type === group);
        return (
          <section key={group} className="mkt-models__group">
            <h3>{t(`settings.models.group.${group}`)}</h3>
            {inGroup.length === 0 ? (
              <p className="mkt-muted">{t("settings.models.empty")}</p>
            ) : (
              <div className="mkt-models__grid">
                {inGroup.map((m) => (
                  <ModelCard
                    key={m.id}
                    model={m}
                    onDownload={() => download(m.id)}
                    onDelete={() => remove(m.id)}
                    onActivate={() => activate(m.id)}
                    onTest={() => test(m.id)}
                  />
                ))}
              </div>
            )}
          </section>
        );
      })}
    </div>
  );
}

interface ModelCardProps {
  model: Model;
  onDownload: () => void;
  onDelete: () => void;
  onActivate: () => void;
  onTest: () => void;
}

function ModelCard({ model, onDownload, onDelete, onActivate, onTest }: ModelCardProps) {
  const { t } = useI18n();
  const m = model;
  const isCloud = m.platform === "cloud";
  const isDownloaded = m.status === "downloaded" || m.status === "active";

  return (
    <Card elevation={1} className="mkt-models__card">
      <div className="mkt-models__card-head">
        <strong>{m.name}</strong>
        <Badge tone={STATUS_TONE[m.status]}>{t(`settings.models.status.${m.status}`)}</Badge>
      </div>

      <div className="mkt-models__meta">
        {!isCloud && <span className="mkt-models__size">{m.size}</span>}
        <Badge tone="neutral">{m.platform}</Badge>
      </div>

      {m.status === "downloading" && (
        <ProgressBar value={m.progress} label={t("settings.models.status.downloading")} />
      )}

      {m.last_test && (
        <p className={m.last_test.ok ? "mkt-models__test-ok" : "mkt-models__test-fail"}>
          {m.last_test.ok
            ? t("settings.models.testOk", { ms: m.last_test.latency_ms })
            : t("settings.models.testFail")}
        </p>
      )}

      <div className="mkt-models__actions">
        {!isDownloaded && !isCloud && m.status !== "downloading" && (
          <Button variant="primary" size="sm" onClick={onDownload}>
            {t("settings.models.download")}
          </Button>
        )}
        {isDownloaded && !m.active && (
          <Button variant="secondary" size="sm" onClick={onActivate}>
            {t("settings.models.activate")}
          </Button>
        )}
        {isDownloaded && (
          <Button variant="ghost" size="sm" onClick={onTest}>
            {t("settings.models.test")}
          </Button>
        )}
        {isDownloaded && !isCloud && (
          <Button variant="destructive" size="sm" onClick={onDelete}>
            {t("settings.models.delete")}
          </Button>
        )}
      </div>
    </Card>
  );
}
