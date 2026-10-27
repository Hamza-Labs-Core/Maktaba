// Epic 45 — pin a library to the sidebar: a compact pin icon toggle (outline
// when not pinned, filled when pinned), never a text button in a row of actions.
// It sits by the library's title on its page and in the corner of its card.
//
// The icon changes at once (lib/pins publishes the new list before the server
// answers; a refusal puts the old one back) and a short toast says what happened.
// The tooltip and the accessible name say what a click does; aria-pressed says
// the state. With the pins full (MAX_PINS) the button stays reachable and its
// click explains why nothing was pinned, rather than silently doing nothing.
import { useState } from "react";
import { Tooltip } from "@ds/components/Tooltip/Tooltip";
import { useToast } from "@ds/components/Toast/Toast";
import { useAuth } from "../lib/auth";
import { useI18n } from "../lib/i18n";
import type { LibraryRef } from "../lib/browse";
import { MAX_PINS, togglePin, usePins } from "../lib/pins";
import { errorText } from "../lib/errorText";

export function PinIcon({ filled }: { filled: boolean }) {
  return (
    <svg
      viewBox="0 0 24 24"
      width="20"
      height="20"
      aria-hidden="true"
      focusable="false"
      fill={filled ? "currentColor" : "none"}
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinejoin="round"
      strokeLinecap="round"
    >
      <path d="M8 3.5h8M9.5 3.5v5.2L6.5 12.5v1.5h11v-1.5l-3-3.8V3.5" />
      <path d="M12 14v6.5" fill="none" />
    </svg>
  );
}

export function PinToggle({ lib, className }: { lib: LibraryRef; className?: string }) {
  const { t } = useI18n();
  const { user } = useAuth();
  const toast = useToast();
  const pins = usePins(user?.id);
  const [busy, setBusy] = useState(false);
  const pinned = !!pins?.some((p) => p.id === lib.id);
  const full = !pinned && (pins?.length ?? 0) >= MAX_PINS;

  const label = full
    ? t("libraries.pin.fullNamed", { name: lib.name, max: MAX_PINS })
    : t(pinned ? "libraries.pinnedNamed" : "libraries.pinToSidebar", { name: lib.name });
  const tip = full
    ? t("libraries.pin.full", { max: MAX_PINS })
    : t(pinned ? "libraries.pinned.tip" : "libraries.pin.tip");

  async function onClick() {
    if (!pins || busy) return;
    if (full) {
      toast.show({ tone: "info", message: t("libraries.pin.fullHelp", { max: MAX_PINS }) });
      return;
    }
    setBusy(true);
    try {
      await togglePin(lib, pins);
      toast.show({
        tone: "success",
        message: t(pinned ? "libraries.unpinned" : "libraries.pinnedDone", { name: lib.name }),
      });
    } catch (e) {
      toast.show({
        tone: "error",
        message: `${t("libraries.pin.error")}: ${errorText(e, t)}`,
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Tooltip label={tip} side="bottom" className={className}>
      <button
        type="button"
        className={`mkt-pin${pinned ? " mkt-pin--on" : ""}`}
        aria-pressed={pinned}
        aria-label={label}
        aria-disabled={!pins || full || undefined}
        onClick={() => void onClick()}
      >
        <PinIcon filled={pinned} />
      </button>
    </Tooltip>
  );
}
