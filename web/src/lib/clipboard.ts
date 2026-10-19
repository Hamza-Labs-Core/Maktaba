// Copy text on any origin. navigator.clipboard only exists in a secure
// context (https or localhost), and Maktaba is usually opened over plain
// http on the LAN (http://192.168.1.20:8780), where it is undefined. Then
// the text is selected in a read-only field and document.execCommand("copy")
// copies the selection, which still works on http. When even that fails the
// field stays selected, so Ctrl+C / long-press copies it by hand.

/** Select all of a field's text (the fallback, and what a failed copy leaves). */
export function selectField(field: HTMLInputElement | HTMLTextAreaElement | null): void {
  if (!field) return;
  field.focus();
  field.select();
  try {
    field.setSelectionRange(0, field.value.length);
  } catch {
    // some input types refuse a selection range; select() already ran
  }
}

function execCopy(field: HTMLInputElement | HTMLTextAreaElement | null, text: string): boolean {
  let target = field;
  let temp: HTMLTextAreaElement | null = null;
  if (!target) {
    temp = document.createElement("textarea");
    temp.value = text;
    temp.setAttribute("readonly", "");
    temp.style.position = "fixed";
    temp.style.opacity = "0";
    document.body.appendChild(temp);
    target = temp;
  }
  selectField(target);
  let ok = false;
  try {
    ok = typeof document.execCommand === "function" && document.execCommand("copy");
  } catch {
    ok = false;
  } finally {
    if (temp) temp.remove();
  }
  return ok;
}

/**
 * Copy ``text``: the async clipboard where the page may use it, else the
 * selection of ``field`` (or a hidden one) through execCommand("copy").
 * Resolves true when the text was copied.
 */
export async function copyText(
  text: string,
  field?: HTMLInputElement | HTMLTextAreaElement | null
): Promise<boolean> {
  const secure = typeof window !== "undefined" && window.isSecureContext !== false;
  const clip = typeof navigator !== "undefined" ? navigator.clipboard : undefined;
  if (secure && clip && typeof clip.writeText === "function") {
    try {
      await clip.writeText(text);
      return true;
    } catch {
      // permission refused or not focused: try the selection below
    }
  }
  return execCopy(field ?? null, text);
}
