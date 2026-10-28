// The Manage libraries confirmations: Empty trash, Merge and Remove. Merge and
// Remove show what the library takes with it (GET …/impact) and ask for the
// library's name typed in full before they act.
import { useEffect, useState } from "react";
import { Modal } from "@ds/components/Modal/Modal";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { useToast } from "@ds/components/Toast/Toast";
import {
  libraryAdminApi,
  problemMessage,
  type Impact,
  type ManagedLibrary,
} from "../../../lib/libraryAdminApi";
import { bidiIsolate, useI18n } from "../../../lib/i18n";

const IMPACT_KEYS: Array<keyof Impact> = [
  "videos",
  "transcripts",
  "playback_states",
  "watch_sessions",
  "series",
  "collections",
  "channel_blocks",
];

function ImpactList({ id }: { id: string }) {
  const { t, n } = useI18n();
  const [impact, setImpact] = useState<Impact | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let alive = true;
    libraryAdminApi
      .impact(id)
      .then((i) => alive && setImpact(i))
      .catch((e) => alive && setError(problemMessage(e, t("common.error"))));
    return () => {
      alive = false;
    };
  }, [id, t]);
  if (error)
    return (
      <p className="mkt-error" role="alert">
        {t("libraryAdmin.impact.failed")}: {error}
      </p>
    );
  if (!impact) return <p className="mkt-loading">{t("common.loading")}</p>;
  return (
    <>
      <ul className="mkt-libadmin__impact" aria-label={t("libraryAdmin.impact.title")}>
        {IMPACT_KEYS.map((k) => (
          <li key={k}>
            <strong>{n(impact[k])}</strong> {t(`libraryAdmin.impact.${k}`)}
          </li>
        ))}
      </ul>
      {/* grants are never carried over, a merge included */}
      {impact.grants > 0 && (
        <p className="mkt-storage__warn">
          {t("libraryAdmin.impact.grants", { count: n(impact.grants) })}
        </p>
      )}
    </>
  );
}

/** The "type the name to confirm" field; matches only the name exactly. */
// A typed name as a person means it: the same letters whatever their Unicode spelling,
// spaces at the ends or between words, or the invisible direction marks a right-to-left
// name picks up when copied (the server reads it the same way: handlers/libraries
// sameName). Letter case still counts.
export function nameKey(s: string): string {
  return s
    .normalize("NFC")
    .replace(/[\u200e\u200f\u202a-\u202e\u2066-\u2069\ufeff]/g, "")
    .trim()
    .split(/\s+/)
    .join(" ");
}

export function sameName(typed: string, name: string): boolean {
  return nameKey(typed) === nameKey(name);
}

function TypeName({
  name,
  value,
  onChange,
  other,
}: {
  name: string;
  value: string;
  onChange: (v: string) => void;
  /** the library the action goes to, whose name is the wrong one to type */
  other?: string;
}) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);
  const typedOther =
    !!other && value.trim() !== "" && sameName(value, other) && !sameName(value, name);
  return (
    <div className="mkt-typename">
      <p className="mkt-typename__ask">
        {t("libraryAdmin.confirm.typeExactly")}{" "}
        <code className="mkt-typename__name" dir="auto">
          {name}
        </code>{" "}
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={() => {
            void navigator.clipboard?.writeText(name).then(
              () => setCopied(true),
              () => undefined
            );
          }}
        >
          {copied ? t("libraryAdmin.confirm.copied") : t("libraryAdmin.confirm.copy")}
        </Button>
      </p>
      <Input
        label={t("libraryAdmin.confirm.typeName", { name: bidiIsolate(name) })}
        value={value}
        autoComplete="off"
        spellCheck={false}
        dir="auto"
        onChange={(e) => onChange(e.target.value)}
      />
      {typedOther && (
        <p className="mkt-typename__hint" role="status">
          {t("libraryAdmin.confirm.typedOther", { name: bidiIsolate(name) })}
        </p>
      )}
    </div>
  );
}

export function EmptyTrashDialog({
  library,
  onClose,
  onDone,
}: {
  library: ManagedLibrary;
  onClose: () => void;
  onDone: () => void;
}) {
  const { t, n } = useI18n();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  async function confirm() {
    setBusy(true);
    try {
      const r = await libraryAdminApi.emptyTrash(library.id);
      toast.show({
        tone: "success",
        message: t("libraryAdmin.trash.done", { count: n(r?.removed ?? 0) }),
      });
      onDone();
    } catch (e) {
      toast.show({ tone: "error", message: problemMessage(e, t("common.error")) });
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onClose={onClose}
      title={t("libraryAdmin.trash.title")}
      footer={
        <>
          <Button type="button" variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button type="button" variant="destructive" onClick={() => void confirm()} loading={busy}>
            {t("libraryAdmin.trash.confirm")}
          </Button>
        </>
      }
    >
      <p>
        {t("libraryAdmin.trash.body", {
          count: n(library.counts.missing),
          name: bidiIsolate(library.name),
        })}
      </p>
    </Modal>
  );
}

export function MergeDialog({
  library,
  libraries,
  target,
  onClose,
  onDone,
}: {
  /** the library merged away (it is deleted) */
  library: ManagedLibrary;
  libraries: ManagedLibrary[];
  /** the suggested library to merge into */
  target?: string;
  onClose: () => void;
  onDone: () => void;
}) {
  const { t, n } = useI18n();
  const toast = useToast();
  const others = libraries.filter((l) => l.id !== library.id);
  const [into, setInto] = useState(target ?? others[0]?.id ?? "");
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const intoName = others.find((l) => l.id === into)?.name ?? "";

  async function confirm() {
    setBusy(true);
    setError(null);
    try {
      const r = await libraryAdminApi.merge(library.id, into, nameKey(typed));
      toast.show({
        tone: "success",
        message: t("libraryAdmin.merge.done", {
          moved: n(r?.moved ?? 0),
          duplicates: n(r?.duplicates_removed ?? 0),
          name: intoName,
        }),
      });
      onDone();
    } catch (e) {
      setError(problemMessage(e, t("common.error")));
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      dismissable={false}
      title={t("libraryAdmin.merge.title", { name: bidiIsolate(library.name) })}
      footer={
        <>
          <Button type="button" variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            type="button"
            variant="destructive"
            onClick={() => void confirm()}
            loading={busy}
            disabled={!into || !sameName(typed, library.name)}
          >
            {t("libraryAdmin.merge.confirm")}
          </Button>
        </>
      }
    >
      <div className="mkt-form">
        <Select
          label={t("libraryAdmin.merge.into")}
          value={into}
          onChange={(e) => setInto(e.target.value)}
          options={others.map((l) => ({
            value: l.id,
            label: `${l.name} (${t("libraryAdmin.counts.videos", { count: n(l.counts.total) })})`,
          }))}
        />
        <p>
          {t("libraryAdmin.merge.body", {
            name: bidiIsolate(library.name),
            into: bidiIsolate(intoName),
          })}
        </p>
        <ImpactList id={library.id} />
        <TypeName name={library.name} value={typed} onChange={setTyped} other={intoName} />
        {error && (
          <p className="mkt-error" role="alert">
            {error}
          </p>
        )}
      </div>
    </Modal>
  );
}

export function RemoveDialog({
  library,
  onClose,
  onDone,
}: {
  library: ManagedLibrary;
  onClose: () => void;
  onDone: () => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function confirm() {
    setBusy(true);
    setError(null);
    try {
      await libraryAdminApi.remove(library.id);
      toast.show({
        tone: "success",
        message: t("libraryAdmin.remove.done", { name: bidiIsolate(library.name) }),
      });
      onDone();
    } catch (e) {
      setError(problemMessage(e, t("common.error")));
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      dismissable={false}
      title={t("libraryAdmin.remove.title", { name: bidiIsolate(library.name) })}
      footer={
        <>
          <Button type="button" variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            type="button"
            variant="destructive"
            onClick={() => void confirm()}
            loading={busy}
            disabled={!sameName(typed, library.name)}
          >
            {t("libraryAdmin.remove.confirm")}
          </Button>
        </>
      }
    >
      <div className="mkt-form">
        <p>{t("libraryAdmin.remove.body")}</p>
        <p>
          <strong>{t("libraryAdmin.remove.filesStay")}</strong>
        </p>
        <ImpactList id={library.id} />
        <TypeName name={library.name} value={typed} onChange={setTyped} />
        {error && (
          <p className="mkt-error" role="alert">
            {error}
          </p>
        )}
      </div>
    </Modal>
  );
}
