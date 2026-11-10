// Epic 59 story 59.06: speech to text in Settings → This machine (admins). On or off and the
// model, as the hardware tier picks them and why (Light: off until turned on, then base in
// the quiet hours only; no AVX2 or ARM: a smaller model; a CUDA GPU: the largest), each
// model's size and the note that smaller ones make more mistakes, how fast this machine
// measured it transcribes, and how long the audio waiting would take, with the next smaller
// model's estimate when that is over 30 days. Changes apply when the server starts again.
//
//   GET  /api/server/speech          the document ({available: false} without a supervisor)
//   PUT  /api/server/speech          {speech: "on"|"off"|"auto", model: <size>|"auto"}
//   POST /api/server/speech/measure  measure again
import { useCallback, useEffect, useRef, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Select } from "@ds/components/Select/Select";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { serverSettingsApi, type SpeechDoc } from "../../lib/serverSettingsApi";

type Available = Extract<SpeechDoc, { available: true }>;

/** A measure's error that says the model could not be had (no internet, Hugging Face down). */
export function looksOffline(error: string): boolean {
  return /offline|LocalEntryNotFound|connect|resolve|timed out|download|HTTP/i.test(error);
}

export function SpeechSection({
  restarting,
  onRestart,
}: {
  restarting: boolean;
  onRestart: () => void;
}) {
  const { t, n } = useI18n();
  const toast = useToast();
  const [doc, setDoc] = useState<SpeechDoc | null>(null);
  const [busy, setBusy] = useState(false);
  const alive = useRef(true);

  const load = useCallback(async () => {
    try {
      const d = await serverSettingsApi.speech();
      if (alive.current) setDoc(d);
    } catch {
      if (alive.current) setDoc(null);
    }
  }, []);

  useEffect(() => {
    alive.current = true;
    void load();
    return () => {
      alive.current = false;
    };
  }, [load, restarting]);

  if (!doc || !doc.available) return null;
  const d: Available = doc;
  const s = d.speech;
  const one = (v: number) => n(Math.round(v * 10) / 10);
  const sizeOf = (name: string) => d.models.find((m) => m.name === name)?.download_mb;
  const modelLabel = (name: string) => {
    const mb = sizeOf(name);
    return mb ? t("machine.speech.modelOption", { model: name, size: n(mb) }) : name;
  };

  async function change(body: Parameters<typeof serverSettingsApi.saveSpeech>[0]) {
    setBusy(true);
    try {
      setDoc(await serverSettingsApi.saveSpeech(body));
      toast.show({ tone: "success", message: t("machine.speech.saved") });
    } catch (err) {
      toast.show({
        tone: "error",
        message: err instanceof ApiError ? err.problem.title : t("common.error"),
      });
    } finally {
      setBusy(false);
    }
  }

  async function measure() {
    setBusy(true);
    try {
      setDoc(await serverSettingsApi.measureSpeech());
    } catch (err) {
      toast.show({
        tone: "error",
        message: err instanceof ApiError ? err.problem.title : t("common.error"),
      });
    } finally {
      setBusy(false);
    }
  }

  const m = d.measure;
  const measured = m && !m.error && m.model === s.repo && (m.rtf ?? 0) > 0 ? m : null;
  const advice = d.advice;
  const off = !s.on;
  const lock = busy || restarting;

  return (
    <section
      className="mkt-storage__reach"
      aria-labelledby="mkt-machine-speech"
      data-testid="speech-section"
    >
      <h3 id="mkt-machine-speech">
        {t("machine.speech.title")}{" "}
        <Badge>{s.on ? t("machine.speech.on") : t("machine.speech.off")}</Badge>
      </h3>
      {s.engine === "mlx" ? (
        <p className="mkt-muted">{t("machine.speech.mlx")}</p>
      ) : (
        <>
          {off && s.reason === "light" && <p>{t("machine.speech.offLight")}</p>}
          {s.reason === "no_avx2" && <p data-testid="speech-reason">{t("stt.no_avx2")}</p>}
          {s.reason === "arm" && <p data-testid="speech-reason">{t("stt.arm")}</p>}
          {s.reason === "cuda" && <p data-testid="speech-reason">{t("stt.cuda")}</p>}
          {s.on && s.quiet_only && d.quiet_hours && (
            <p className="mkt-muted mkt-libadmin__small">
              {t("machine.speech.quietOnly", { window: d.quiet_hours })}
            </p>
          )}
          {off ? (
            <Button
              variant="primary"
              disabled={lock}
              onClick={() =>
                void change({ speech: "on", model: s.reason === "light" ? s.default : "auto" })
              }
            >
              {s.reason === "light"
                ? t("machine.speech.turnOnWith", { model: s.default })
                : t("machine.speech.turnOn")}
            </Button>
          ) : (
            <>
              <Select
                label={t("machine.speech.model")}
                value={s.model_set ? s.model : "auto"}
                disabled={lock}
                onChange={(e) => void change({ model: e.currentTarget.value })}
                options={[
                  {
                    value: "auto",
                    label: t("machine.speech.auto", { model: modelLabel(s.default) }),
                  },
                  ...s.offered.map((name) => ({ value: name, label: modelLabel(name) })),
                ]}
              />
              <p className="mkt-muted mkt-libadmin__small">{t("stt.model.note")}</p>
              <Button
                variant="ghost"
                disabled={lock}
                onClick={() => void change({ speech: "off" })}
              >
                {t("machine.speech.turnOff")}
              </Button>
            </>
          )}
          {!s.second_listen && (
            <p className="mkt-muted mkt-libadmin__small">
              {t("stt.second_listen.unavailable_arm")}
            </p>
          )}
          {s.on && (
            <div data-testid="speech-speed">
              {measured ? (
                <p>
                  {t((measured.rtf ?? 0) >= 1 ? "stt.speed.faster" : "stt.speed.slower", {
                    model: s.model,
                    x: one(measured.rtf ?? 0),
                  })}
                  {measured.busy && (
                    <span className="mkt-muted mkt-libadmin__small" style={{ display: "block" }}>
                      {t("stt.speed.busy")}
                    </span>
                  )}
                </p>
              ) : d.measuring ? (
                <p className="mkt-muted">{t("machine.speech.measuring")}</p>
              ) : m?.error && m.model === s.repo ? (
                <p role="status">
                  {looksOffline(m.error)
                    ? t("stt.model.offline")
                    : t("machine.speech.measureFailed", { error: m.error })}
                </p>
              ) : (
                <p className="mkt-muted">{t("machine.speech.notMeasured")}</p>
              )}
              {s.engine === "ct2" && !d.restart_needed && (
                <Button
                  variant="secondary"
                  disabled={lock || d.measuring}
                  onClick={() => void measure()}
                >
                  {t("machine.speech.measure")}
                </Button>
              )}
            </div>
          )}
          {advice?.backlog && advice.backlog.hours > 0 && (
            <div data-testid="speech-backlog">
              {advice.estimate ? (
                <p>
                  {t("stt.backlog.estimate", {
                    hours: one(advice.backlog.hours),
                    days: one(advice.estimate.days),
                  })}
                  {advice.estimate.hours_per_day < 24 && (
                    <span className="mkt-muted mkt-libadmin__small" style={{ display: "block" }}>
                      {t("machine.speech.perDay", {
                        window: d.quiet_hours,
                        hours: one(advice.estimate.hours_per_day),
                      })}
                    </span>
                  )}
                </p>
              ) : (
                <p className="mkt-muted">
                  {t("machine.speech.waiting", { hours: one(advice.backlog.hours) })}
                </p>
              )}
              {advice.suggest && (
                <p role="status">
                  {t("stt.backlog.suggest", {
                    model: advice.suggest.model,
                    days: one(advice.suggest.days),
                  })}{" "}
                  <Button
                    variant="secondary"
                    disabled={lock}
                    onClick={() => void change({ model: advice.suggest?.model })}
                  >
                    {t("machine.speech.use", { model: advice.suggest.model })}
                  </Button>
                </p>
              )}
              {advice.suggest_off && <p role="status">{t("machine.speech.suggestOff")}</p>}
            </div>
          )}
        </>
      )}
      {d.restart_needed && (
        <p role="status" className="mkt-muted">
          {restarting ? t("machine.tier.restarting") : t("machine.speech.restartNeeded")}{" "}
          {d.can_restart && !restarting && (
            <Button variant="primary" onClick={onRestart}>
              {t("machine.tier.restart")}
            </Button>
          )}
        </p>
      )}
    </section>
  );
}
