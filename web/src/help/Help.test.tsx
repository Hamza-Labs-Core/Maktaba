import { afterEach, describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { I18nProvider } from "../lib/i18n";
import { HelpIndex, HelpTopic } from "./Help";
import { HelpLink, PageHelp } from "./HelpLink";
import { causeHelp } from "./registry";
import { fold, searchHelp } from "./search";
import { HelpMarkdown } from "./HelpMarkdown";
import { HELP_TOPICS, type HelpTopicId } from "./topics";
import { loadTopic } from "./content";

const FILES = import.meta.glob<string>("./content/*/*.md", {
  query: "?raw",
  import: "default",
  eager: true,
});

function Where() {
  const { pathname, search, hash } = useLocation();
  return <output data-testid="where">{pathname + search + hash}</output>;
}

function renderAt(url: string, locale: "en" | "ar" = "en") {
  localStorage.setItem("mkt:locale", locale);
  return render(
    <I18nProvider>
      <ToastProvider>
        <MemoryRouter initialEntries={[url]}>
          <Routes>
            <Route path="/help" element={<HelpIndex />} />
            <Route path="/help/:topic" element={<HelpTopic />} />
            <Route
              path="/queue"
              element={
                <>
                  <PageHelp />
                  <HelpLink to={causeHelp("nas_timeout")} />
                </>
              }
            />
          </Routes>
          <Where />
        </MemoryRouter>
      </ToastProvider>
    </I18nProvider>
  );
}

afterEach(() => localStorage.clear());

describe("help area", () => {
  it("lists every category on /help", () => {
    renderAt("/help");
    expect(screen.getByRole("heading", { level: 1, name: "Help" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Processing" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /^Failures/ })).toHaveAttribute(
      "href",
      "/help/failures"
    );
  });

  it("searches as you type and keeps the words in the URL", async () => {
    renderAt("/help");
    fireEvent.change(screen.getByRole("searchbox", { name: "Search the help" }), {
      target: { value: "NAS read timed out" },
    });
    const results = await screen.findByRole("region", { name: "Search results" });
    const hit = within(results)
      .getAllByRole("link")
      .find((a) => a.getAttribute("href") === "/help/failures#nas-timeout");
    expect(hit).toBeDefined();
    expect(screen.getByTestId("where")).toHaveTextContent("/help?q=NAS+read+timed+out");
  });

  it("opens a topic at its anchor, highlighted, in Arabic", async () => {
    renderAt("/help/failures#nas-timeout", "ar");
    const h = await waitFor(() => {
      const el = document.getElementById("nas-timeout");
      expect(el).not.toBeNull();
      return el!;
    });
    expect(h).toHaveClass("mkt-help__h--target");
    expect(document.documentElement.dir).toBe("rtl");
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("الإخفاقات");
    expect(screen.getByRole("navigation", { name: "في هذه الصفحة" })).toBeInTheDocument();
  });

  it.each(["nope", "__proto__", "constructor", "toString", "hasOwnProperty"])(
    "says so for a topic that does not exist: %s",
    async (topic) => {
      renderAt(`/help/${topic}`);
      expect(screen.getByRole("heading", { name: "There is no such help topic" })).toBeVisible();
      expect(await loadTopic("en", topic as HelpTopicId)).toBeNull();
    }
  );
});

describe("help links", () => {
  it("the page '?' and a cause's Learn more are plain shareable links", () => {
    renderAt("/queue");
    expect(screen.getByRole("link", { name: "Help: Processing" })).toHaveAttribute(
      "href",
      "/help/processing"
    );
    expect(screen.getByRole("link", { name: "Learn more" })).toHaveAttribute(
      "href",
      "/help/failures#nas-timeout"
    );
  });
});

describe("search", () => {
  it("folds Arabic letter forms and diacritics", () => {
    expect(fold("إعادة")).toBe(fold("اعاده"));
    expect(fold("المُحاولة")).toBe(fold("المحاوله"));
  });

  it("needs every word and ranks headings first", () => {
    const topics = [
      {
        id: "failures" as const,
        title: "Failures",
        src: "# Failures\n\n## What it is {#what-it-is}\n\nSome retry text.\n\n### Retry {#retry}\n\nPress it.",
      },
    ];
    const hits = searchHelp(topics, "retry");
    expect(hits[0]?.anchor).toBe("retry");
    expect(searchHelp(topics, "retry missing")).toEqual([]);
  });
});

describe("every topic renders", () => {
  it.each(HELP_TOPICS.flatMap((id) => [["en", id] as const, ["ar", id] as const]))(
    "%s/%s",
    (locale, id) => {
      localStorage.setItem("mkt:locale", locale);
      const { container, unmount } = render(
        <I18nProvider>
          <ToastProvider>
            <MemoryRouter>
              <HelpMarkdown
                source={FILES[`./content/${locale}/${id}.md`]!}
                topicUrl={`/help/${id}`}
                active=""
              />
            </MemoryRouter>
          </ToastProvider>
        </I18nProvider>
      );
      expect(container.querySelector("#troubleshooting")).not.toBeNull();
      expect(container.textContent).not.toMatch(/\{#[a-z0-9-]+\}/);
      unmount();
    }
  );
});
