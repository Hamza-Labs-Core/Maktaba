// Release notes are rendered as Markdown without ever injecting HTML.
import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { Markdown, safeHref } from "./markdown";

describe("Markdown", () => {
  it("renders headings, lists, emphasis, code and links", () => {
    const { container } = render(
      <Markdown
        source={[
          "## What's new",
          "",
          "- **Faster** scans",
          "- Fixed `ffprobe` on *Windows*",
          "",
          "1. one",
          "2. two",
          "",
          "See [the docs](https://example.com/docs) or https://example.com/x.",
          "",
          "```",
          "docker compose pull",
          "```",
        ].join("\n")}
      />
    );
    expect(screen.getByRole("heading", { name: "What's new" }).tagName).toBe("H4");
    expect(container.querySelectorAll("ul > li")).toHaveLength(2);
    expect(container.querySelectorAll("ol > li")).toHaveLength(2);
    expect(screen.getByText("Faster").tagName).toBe("STRONG");
    expect(screen.getByText("Windows").tagName).toBe("EM");
    expect(screen.getByText("ffprobe").tagName).toBe("CODE");
    expect(screen.getByRole("link", { name: "the docs" })).toHaveAttribute(
      "href",
      "https://example.com/docs"
    );
    expect(screen.getByRole("link", { name: "https://example.com/x" })).toHaveAttribute(
      "href",
      "https://example.com/x"
    );
    expect(container.querySelector("pre code")?.textContent).toBe("docker compose pull");
  });

  it("shows raw HTML as text and drops unsafe link schemes", () => {
    const { container } = render(
      <Markdown
        source={
          '<img src=x onerror="alert(1)"> <script>alert(2)</script>\n\n[click](javascript:alert(3))'
        }
      />
    );
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("script")).toBeNull();
    expect(container.textContent).toContain('<img src=x onerror="alert(1)">');
    expect(screen.queryByRole("link")).toBeNull();
    expect(screen.getByText("click")).toBeInTheDocument();
  });

  it("safeHref keeps only http(s) and mailto", () => {
    expect(safeHref("https://a.b")).toBe("https://a.b");
    expect(safeHref("mailto:x@y.z")).toBe("mailto:x@y.z");
    expect(safeHref("javascript:alert(1)")).toBeNull();
    expect(safeHref("data:text/html,hi")).toBeNull();
  });
});
