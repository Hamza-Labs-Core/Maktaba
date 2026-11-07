// ruleKinds.weekdayNames (#697): Monday first, in the locale, made once per locale.
import { describe, it, expect } from "vitest";
import { weekdayNames, WEEKDAYS } from "./ruleKinds";

describe("weekdayNames", () => {
  it("names the week from Monday in the locale", () => {
    const en = weekdayNames("en");
    expect(en).toHaveLength(WEEKDAYS.length);
    expect(en[0]).toEqual({ short: "Mon", long: "Monday" });
    expect(en[6]).toEqual({ short: "Sun", long: "Sunday" });
    expect(weekdayNames("ar")[0]!.long).toBe("الاثنين");
  });

  it("is made once per locale, not on every call", () => {
    expect(weekdayNames("en")).toBe(weekdayNames("en"));
    expect(weekdayNames("ar")).not.toBe(weekdayNames("en"));
  });
});
