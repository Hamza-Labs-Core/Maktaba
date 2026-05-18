import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { IconButton } from "./IconButton/IconButton";
import { Link } from "./Link/Link";
import { Badge } from "./Badge/Badge";
import { Chip } from "./Chip/Chip";
import { Avatar } from "./Avatar/Avatar";
import { Skeleton } from "./Skeleton/Skeleton";
import { ProgressBar } from "./ProgressBar/ProgressBar";
import { Select } from "./Select/Select";
import { Textarea } from "./Textarea/Textarea";
import { EmptyState } from "./EmptyState/EmptyState";
import { ErrorState } from "./ErrorState/ErrorState";
import { Tooltip } from "./Tooltip/Tooltip";
import { Drawer } from "./Drawer/Drawer";

const dot = <svg viewBox="0 0 1 1" />;

describe("IconButton", () => {
  it("uses label as the accessible name and is clickable", async () => {
    const onClick = vi.fn();
    render(<IconButton label="Play" icon={dot} onClick={onClick} />);
    const btn = screen.getByRole("button", { name: "Play" });
    await userEvent.setup().click(btn);
    expect(onClick).toHaveBeenCalled();
  });
});

describe("Link", () => {
  it("adds safe rel + new-tab hint when external", () => {
    render(
      <Link href="https://x.test" external>
        Docs
      </Link>
    );
    const a = screen.getByRole("link", { name: /Docs/ });
    expect(a).toHaveAttribute("rel", "noopener noreferrer");
    expect(a).toHaveAttribute("target", "_blank");
    expect(a).toHaveTextContent("opens in new tab");
  });
});

describe("Badge / Chip", () => {
  it("Badge renders its tone class", () => {
    render(<Badge tone="success">Done</Badge>);
    expect(screen.getByText("Done").className).toContain("mk-badge--success");
  });
  it("Chip remove button is keyboard-operable", async () => {
    const onRemove = vi.fn();
    render(
      <Chip onRemove={onRemove} removeLabel="Remove tag">
        Arabic
      </Chip>
    );
    await userEvent.setup().click(screen.getByRole("button", { name: "Remove tag" }));
    expect(onRemove).toHaveBeenCalled();
  });
});

describe("Avatar", () => {
  it("falls back to initials when no src", () => {
    render(<Avatar name="Mahmoud Darwish" />);
    const img = screen.getByRole("img", { name: "Mahmoud Darwish" });
    expect(img).toHaveTextContent("MD");
  });
  it("shows initials after image error", () => {
    render(<Avatar name="Test User" src="/broken.png" />);
    fireEvent.error(document.querySelector("img")!);
    expect(screen.getByRole("img", { name: "Test User" })).toHaveTextContent("TU");
  });
});

describe("Skeleton", () => {
  it("is hidden from assistive tech", () => {
    const { container } = render(<Skeleton variant="rect" />);
    expect(container.firstChild).toHaveAttribute("aria-hidden", "true");
  });
});

describe("ProgressBar", () => {
  it("exposes determinate value via ARIA", () => {
    render(<ProgressBar value={42} label="Indexing" />);
    const bar = screen.getByRole("progressbar", { name: "Indexing" });
    expect(bar).toHaveAttribute("aria-valuenow", "42");
  });
  it("omits aria-valuenow when indeterminate", () => {
    render(<ProgressBar indeterminate label="Working" />);
    expect(screen.getByRole("progressbar")).not.toHaveAttribute("aria-valuenow");
  });
});

describe("Select / Textarea", () => {
  it("Select renders options and is labelled", () => {
    render(
      <Select
        label="Quality"
        options={[
          { value: "lo", label: "Low" },
          { value: "hi", label: "High" },
        ]}
      />
    );
    const sel = screen.getByLabelText("Quality");
    expect(sel.querySelectorAll("option")).toHaveLength(2);
  });
  it("Textarea wires its error via role=alert", () => {
    render(<Textarea label="Notes" error="Too long" />);
    expect(screen.getByLabelText("Notes")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByRole("alert")).toHaveTextContent("Too long");
  });
});

describe("EmptyState / ErrorState", () => {
  it("EmptyState tags its kind and renders the CTA", () => {
    render(
      <EmptyState
        kind="filtered_out"
        title="No matches"
        action={<button>Clear filters</button>}
      />
    );
    expect(screen.getByText("No matches").closest("[data-kind]")).toHaveAttribute(
      "data-kind",
      "filtered_out"
    );
    expect(screen.getByRole("button", { name: "Clear filters" })).toBeInTheDocument();
  });
  it("ErrorState is an alert classified by kind", () => {
    render(<ErrorState kind="network" title="Offline" />);
    const alert = screen.getByRole("alert");
    expect(alert).toHaveAttribute("data-kind", "network");
    expect(alert).toHaveTextContent("Offline");
  });
});

describe("Tooltip", () => {
  it("shows on focus and links via aria-describedby", async () => {
    render(
      <Tooltip label="Save changes">
        <button>Save</button>
      </Tooltip>
    );
    const user = userEvent.setup();
    const btn = screen.getByRole("button", { name: "Save" });
    await user.tab();
    expect(btn).toHaveFocus();
    expect(btn).toHaveAttribute("aria-describedby");
    expect(screen.getByRole("tooltip")).toHaveTextContent("Save changes");
  });
});

describe("Drawer", () => {
  it("renders an accessible dialog and closes on Escape", async () => {
    const onClose = vi.fn();
    render(
      <Drawer open onClose={onClose} title="Filters">
        <button>Apply</button>
      </Drawer>
    );
    expect(screen.getByRole("dialog")).toHaveAccessibleName("Filters");
    await userEvent.setup().keyboard("{Escape}");
    expect(onClose).toHaveBeenCalled();
  });
});
