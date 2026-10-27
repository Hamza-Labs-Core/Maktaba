// The video's display title (Story 27.11): the short on-air title the guide
// and the channel strip show, where it came from (the rules, the language
// model, or set by hand), and for an admin an edit box and "back to the
// automatic title". A title set by hand always wins.
import { useEffect, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { api } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { errorText } from "../lib/errorText";

export interface DisplayTitleVideo {
  id: string;
  display_title?: string | null;
  display_title_auto?: string | null;
  display_title_manual?: string | null;
  display_title_source?: string | null;
}

export const DISPLAY_TITLE_MAX = 200;

const KNOWN_SOURCES = new Set(["rules", "llm", "manual"]);

export function DisplayTitle({
  video,
  canEdit,
  onSaved,
}: {
  video: DisplayTitleVideo;
  canEdit: boolean;
  onSaved: (v: DisplayTitleVideo) => void;
}) {
  const { t } = useI18n();
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState(video.display_title ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!editing) setText(video.display_title ?? "");
  }, [video.display_title, editing]);

  const rawSource = video.display_title_source || "";
  // An unknown source (a newer server) gets no badge rather than a raw key.
  const source = KNOWN_SOURCES.has(rawSource) ? rawSource : "";
  if (!video.display_title && !canEdit) return null;

  async function save(value: string | null) {
    setBusy(true);
    setError(null);
    try {
      const v = await api.patch<DisplayTitleVideo>(`/api/videos/${encodeURIComponent(video.id)}`, {
        display_title: value,
      });
      onSaved(v);
      setEditing(false);
    } catch (e) {
      setError(errorText(e, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mkt-video__display" data-testid="display-title">
      <span className="mkt-muted">{t("video.displayTitle")}</span>{" "}
      {editing ? (
        <span className="mkt-video__display-edit">
          <Input
            label={t("video.displayTitle.edit")}
            value={text}
            dir="auto"
            maxLength={DISPLAY_TITLE_MAX}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") setEditing(false);
            }}
          />
          <Button size="sm" disabled={busy || text.trim() === ""} onClick={() => save(text.trim())}>
            {t("common.save")}
          </Button>
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => setEditing(false)}>
            {t("common.cancel")}
          </Button>
        </span>
      ) : (
        <>
          <strong dir="auto">{video.display_title || t("video.displayTitle.none")}</strong>{" "}
          {source && (
            <Badge tone={source === "manual" ? "accent" : "neutral"}>
              {t(`video.displayTitle.source.${source}`)}
            </Badge>
          )}
          {canEdit && (
            <>
              {" "}
              <Button size="sm" variant="ghost" onClick={() => setEditing(true)}>
                {t("video.displayTitle.change")}
              </Button>
              {source === "manual" && (
                <Button size="sm" variant="ghost" disabled={busy} onClick={() => save(null)}>
                  {t("video.displayTitle.reset")}
                </Button>
              )}
            </>
          )}
          {source === "manual" && video.display_title_auto && (
            <span className="mkt-muted" dir="auto">
              {" "}
              {t("video.displayTitle.auto", { title: video.display_title_auto })}
            </span>
          )}
        </>
      )}
      {error && (
        <p className="mkt-form__warn" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
