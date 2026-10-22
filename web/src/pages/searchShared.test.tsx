// renderSnippet: the server's <mark> wrappers become <mark> elements, every
// other run stays plain text, and the whole snippet is one dir="auto" run so
// an Arabic title keeps its spaces in an LTR page.
import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import { renderSnippet } from "./searchShared";

describe("renderSnippet", () => {
  it("keeps the space between a plain word and a marked one, in one bidi run", () => {
    const { container } = render(<p>{renderSnippet("أقم <mark>الصلاة</mark>")}</p>);
    const runs = container.querySelectorAll("[dir]");
    expect(runs).toHaveLength(1);
    expect(runs[0].getAttribute("dir")).toBe("auto");
    expect(runs[0].textContent).toBe("أقم الصلاة");
    expect(container.querySelectorAll("mark")).toHaveLength(1);
    expect(container.querySelector("mark")?.textContent).toBe("الصلاة");
  });

  it("marks several words and leaves other markup as text", () => {
    const { container } = render(
      <p>{renderSnippet("<mark>الصلاة</mark> و <mark>الزكاة</mark> <b>x</b>")}</p>
    );
    expect(container.querySelectorAll("mark")).toHaveLength(2);
    expect(container.querySelector("b")).toBeNull();
    expect(container.textContent).toBe("الصلاة و الزكاة <b>x</b>");
  });

  it("renders nothing without a snippet", () => {
    expect(renderSnippet(null)).toBeNull();
    expect(renderSnippet(undefined)).toBeNull();
  });
});
