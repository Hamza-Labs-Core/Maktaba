import { describe, expect, it } from "vitest";
import { nameKey, sameName } from "./ConfirmDialogs";

// A typed confirmation matches however a person types the name; letter case and another
// name still don't.
describe("sameName", () => {
  const name = "د. عثمان الخميس";
  it.each([name, `  ${name} `, "د.  عثمان   الخميس", name.normalize("NFD"), `⁧${name}⁩`])(
    "matches %j",
    (typed) => {
      expect(sameName(typed, name)).toBe(true);
    }
  );
  it.each(["", "د. عثمان", "Dr. Othman (dup)"])("refuses %j", (typed) => {
    expect(sameName(typed, name)).toBe(false);
  });
  it("letter case still counts", () => {
    expect(sameName("dr. othman", "Dr. Othman")).toBe(false);
  });
  it("sends the server the name as typed, tidied", () => {
    expect(nameKey(`  Dr.   Othman ‏`)).toBe("Dr. Othman");
  });
});
