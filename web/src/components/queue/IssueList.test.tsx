// A "Needs attention" group says its cause in words, in the page's language: never the
// grouping key with its `kind:` and its `<n>` / `<path>`, never a backslash-n, an Arabic
// file name in its own direction. The words are the Android app's (AttentionWords.kt).
import { afterEach, describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider, lookupMessage, type Locale } from "../../lib/i18n";
import {
  CAUSE_CODES,
  groupTitle,
  isolateRtl,
  looksLikeKey,
  readableMessage,
} from "../../lib/attentionWords";
import type { FailureGroup } from "../../lib/queueApi";
import { IssueList } from "./IssueList";

const say = (locale: Locale) => (key: string) => lookupMessage(locale, key) ?? key;
const en = say("en");
const ar = say("ar");

function group(cause: string, over: Partial<FailureGroup> = {}): FailureGroup {
  return {
    stage: "s",
    cause,
    label: cause,
    count: 3,
    first_at: "2026-11-01T00:00:00Z",
    last_at: "2026-11-01T00:00:00Z",
    sample_message: "",
    sample_subjects: [],
    action: "retry",
    ...over,
  };
}

describe("groupTitle", () => {
  it("titles a generated cause by its kind", () => {
    const model = group("model_unavailable:<n> of <n> clips got no answer from the model");
    expect(groupTitle(model, en)).toBe("The model did not answer");
    expect(groupTitle(model, ar)).toBe("لم يُجب النموذج");
    const nas = group(
      "nas_timeout:<path> شاهين/المعجزات والخوارق..برهان النبوة الثاني<n> | دلائل النبوة <n>.mp<n>>: no answer"
    );
    expect(groupTitle(nas, en)).toBe("NAS read timed out");
    expect(groupTitle(nas, ar)).toBe("انتهت مهلة القراءة من NAS");
    // a chapter's frames fail as ffmpeg does
    expect(groupTitle(group("chapter_frames_failed:<n>"), ar)).toBe("فشل ffmpeg في معالجة الملف");
  });

  it("says a known cause in the page's language, not the home's English", () => {
    const g = group("plex_unreachable", { label: "Plex is unreachable" });
    expect(groupTitle(g, en)).toBe("Plex is unreachable");
    expect(groupTitle(g, ar)).toBe("لا يمكن الوصول إلى Plex");
  });

  it("takes the home's label for a cause it has no words for, unless the label is a key", () => {
    // a newer home's cause, labelled by the home (api queues/causes.go)
    const g = group("disk_read_only", { label: "Disk is read-only" });
    expect(groupTitle(g, en)).toBe("Disk is read-only");
    expect(groupTitle(g, ar)).toBe("Disk is read-only");
    // an older home sent the key itself as the label: the kind in words instead
    expect(groupTitle(group("some_new_kind:<n> things"), en)).toBe("Some new kind");
    expect(looksLikeKey("nas_timeout:<path>: no answer")).toBe(true);
    expect(looksLikeKey("NAS read timed out")).toBe(false);
  });

  it("has every cause's words in English and Arabic", () => {
    for (const code of CAUSE_CODES) {
      const key = `queue.issues.cause.${code}`;
      expect(lookupMessage("en", key), key).toBeTruthy();
      expect(lookupMessage("ar", key), key).toMatch(/[؀-ۿ]/);
    }
  });
});

describe("readableMessage", () => {
  it("makes a message's escapes line breaks", () => {
    expect(readableMessage("return code -22 (Invalid argument)\\n[out#0/image2 @ 0x6000]\\n")).toBe(
      "return code -22 (Invalid argument)\n[out#0/image2 @ 0x6000]"
    );
    expect(readableMessage("a\\nb\\r\\nc")).not.toContain("\\");
  });

  it("isolates an Arabic file name in an English line", () => {
    expect(isolateRtl("/Volumes/Media/شاهين/المعجزات.mp4: no answer")).toBe(
      "⁨/Volumes/Media/شاهين/المعجزات.mp4⁩: no answer"
    );
    // nothing right-to-left: as it was
    expect(isolateRtl("read failed: head, tail")).toBe("read failed: head, tail");
  });
});

describe("IssueList", () => {
  afterEach(() => localStorage.removeItem("mkt:locale"));

  function renderList(groups: FailureGroup[]) {
    return render(
      <I18nProvider>
        <MemoryRouter>
          <IssueList groups={groups} stageLabel={(s) => s} onRetry={() => {}} onApply={() => {}} />
        </MemoryRouter>
      </I18nProvider>
    );
  }

  it("shows the cause's words in Arabic and the message on its own lines", () => {
    localStorage.setItem("mkt:locale", "ar");
    const { container } = renderList([
      group("nas_timeout:<path>: no answer", {
        label: "NAS read timed out",
        sample_message: "/Volumes/Media/شاهين/المعجزات.mp4: no answer\\nretry later",
      }),
    ]);
    expect(screen.getByText("انتهت مهلة القراءة من NAS")).toBeInTheDocument();
    expect(screen.queryByText(/NAS read timed out/)).toBeNull();
    expect(container.querySelector(".mkt-q-issue__msg")?.textContent).toBe(
      "⁨/Volumes/Media/شاهين/المعجزات.mp4⁩: no answer\nretry later"
    );
  });
});
