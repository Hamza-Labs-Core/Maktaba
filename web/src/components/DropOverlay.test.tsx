import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { DropOverlay } from "./DropOverlay";

describe("DropOverlay", () => {
  it("is not rendered when inactive", () => {
    const { container } = render(<DropOverlay active={false} libraryName="Lectures" />);
    expect(container.firstChild).toBeNull();
  });

  it("shows the target library name when active", () => {
    render(<DropOverlay active libraryName="Lectures" />);
    expect(screen.getByText(/Drop here to add to Lectures/i)).toBeInTheDocument();
  });

  it("falls back to a generic label without a library name", () => {
    render(<DropOverlay active />);
    expect(screen.getByText(/Drop here to add/i)).toBeInTheDocument();
  });

  it("exposes a status role for assistive tech", () => {
    render(<DropOverlay active libraryName="Lectures" />);
    expect(screen.getByRole("status")).toBeInTheDocument();
  });

  it("renders a rejection message when files were filtered out", () => {
    render(<DropOverlay active libraryName="Lectures" rejectedCount={2} onDismiss={vi.fn()} />);
    expect(screen.getByText(/2 file\(s\) skipped \(unsupported\)/i)).toBeInTheDocument();
  });
});
