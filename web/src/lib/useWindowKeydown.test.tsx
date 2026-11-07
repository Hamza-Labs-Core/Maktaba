// lib/useWindowKeydown (#697): a key pressed right after a render reaches that render's
// handler, and the window listener is attached once.
import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";
import { useLayoutEffect } from "react";
import { useWindowKeydown } from "./useWindowKeydown";

function Reader({ word, seen }: { word: string; seen: string[] }) {
  useWindowKeydown((e) => seen.push(`${e.key}:${word}`));
  return <p>{word}</p>;
}

// The parent's layout effect runs at the commit that shows the new word, after its
// child's and before any passive effect: the moment a re-attaching listener still
// held the previous render's handler.
function KeyAtCommit({ word, seen }: { word: string; seen: string[] }) {
  useLayoutEffect(() => {
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "n" }));
  }, [word]);
  return <Reader word={word} seen={seen} />;
}

describe("useWindowKeydown", () => {
  it("a key pressed at the commit that shows a word acts on that word", () => {
    const seen: string[] = [];
    const { rerender } = render(<KeyAtCommit word="first" seen={seen} />);
    rerender(<KeyAtCommit word="second" seen={seen} />);
    // the first key comes before any listener is attached (the mount's passive effect)
    expect(seen).toEqual(["n:second"]);
  });

  it("attaches one listener for the component's life, and removes it on unmount", () => {
    const add = vi.spyOn(window, "addEventListener");
    const remove = vi.spyOn(window, "removeEventListener");
    const keydowns = (spy: typeof add) => spy.mock.calls.filter((c) => c[0] === "keydown");
    const seen: string[] = [];
    const { rerender, unmount } = render(<Reader word="a" seen={seen} />);
    rerender(<Reader word="b" seen={seen} />);
    rerender(<Reader word="c" seen={seen} />);
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "x" }));
    expect(seen).toEqual(["x:c"]);
    expect(keydowns(add)).toHaveLength(1);
    expect(keydowns(remove)).toHaveLength(0);
    unmount();
    expect(keydowns(remove)).toHaveLength(1);
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "y" }));
    expect(seen).toEqual(["x:c"]);
    add.mockRestore();
    remove.mockRestore();
  });

  it("listens in the capture phase when asked", () => {
    const add = vi.spyOn(window, "addEventListener");
    function Capture() {
      useWindowKeydown(() => {}, true);
      return null;
    }
    render(<Capture />);
    expect(add.mock.calls.find((c) => c[0] === "keydown")?.[2]).toBe(true);
    add.mockRestore();
  });
});
