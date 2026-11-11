import { describe, it, expect } from "vitest";
import { ApiError } from "./api";
import { errorText } from "./errorText";
import { libraryAccessErrorText, libraryRefusalText } from "./libraryAccess";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";

const tr = (cat: Record<string, string>) => (k: string) => cat[k] ?? k;
const t = tr(en as Record<string, string>);
const refused = (code: string) =>
  new ApiError({ type: "about:blank", title: "conflict", status: 409, code } as never);

// Epic 59 story 59.16: an action a library's access rules out says why to an admin, with the
// fix that may name a share or a uid; anyone else reads only "Not available in this library".
describe("a library's refusal", () => {
  it("an admin reads the reason and the fix", () => {
    expect(libraryRefusalText(true, t)).toBe(en["library.read_only"]);
    expect(libraryRefusalText(true, t, "Give user 1026 Read/Write")).toBe(
      `${en["library.read_only"]}. Give user 1026 Read/Write`
    );
  });

  it("anyone else reads only that it is not available here, never the fix", () => {
    const s = libraryRefusalText(false, t, "Give user 1026 Read/Write");
    expect(s).toBe("Not available in this library");
    expect(s).not.toContain("1026");
  });

  it("a 409 library_read_only or library_no_access, by role", () => {
    for (const code of ["library_read_only", "library_no_access"]) {
      expect(libraryAccessErrorText(refused(code), t)).toBe(t(`error.${code}`));
      expect(libraryAccessErrorText(refused(code), t, false)).toBe(
        en["library.not_available_here"]
      );
      expect(errorText(refused(code), t, { admin: false })).toBe(en["library.not_available_here"]);
      expect(errorText(refused(code), t, { admin: true })).toBe(t(`error.${code}`));
    }
    expect(libraryAccessErrorText(refused("other"), t, false)).toBeNull();
  });

  it("speaks Arabic", () => {
    const tAr = tr(ar as Record<string, string>);
    expect(libraryRefusalText(false, tAr)).toBe("غير متاح في هذه المكتبة");
    expect(libraryAccessErrorText(refused("library_read_only"), tAr, false)).toBe(
      "غير متاح في هذه المكتبة"
    );
  });
});
