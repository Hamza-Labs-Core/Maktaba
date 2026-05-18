import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ToastProvider, useToast } from "./Toast";

function Harness() {
  const { show } = useToast();
  return (
    <>
      <button onClick={() => show({ message: "Saved", tone: "success" })}>emit</button>
      <button onClick={() => show({ id: "dupe", message: "Retry failed", tone: "error" })}>
        dupe
      </button>
    </>
  );
}

describe("Toast", () => {
  it("shows a toast and lets it be dismissed", async () => {
    render(
      <ToastProvider>
        <Harness />
      </ToastProvider>
    );
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "emit" }));
    expect(screen.getByText("Saved")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByText("Saved")).not.toBeInTheDocument();
  });

  it("dedupes toasts that share an id (idempotent retry)", async () => {
    render(
      <ToastProvider>
        <Harness />
      </ToastProvider>
    );
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "dupe" }));
    await user.click(screen.getByRole("button", { name: "dupe" }));
    expect(screen.getAllByText("Retry failed")).toHaveLength(1);
  });

  it("throws if useToast is used outside a provider", () => {
    function Bare() {
      useToast();
      return null;
    }
    expect(() => render(<Bare />)).toThrow(/ToastProvider/);
  });
});
