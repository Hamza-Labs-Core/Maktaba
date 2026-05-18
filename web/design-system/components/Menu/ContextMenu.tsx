import { useEffect, useId, useRef, useState } from "react";
import type { KeyboardEvent, ReactNode } from "react";
import { clsx } from "../internal/clsx";
import type { MenuItem } from "./Menu";
import "./menu.css";

// Story 17.2 — ContextMenu. Wraps arbitrary children; a right-click (or
// the keyboard ContextMenu key) opens an ARIA menu at the pointer. Same
// roving-focus + Escape + outside-click contract as Menu.

export interface ContextMenuProps {
  items: MenuItem[];
  children: ReactNode;
  className?: string;
}

export function ContextMenu({ items, children, className }: ContextMenuProps) {
  const [pos, setPos] = useState<{ x: number; y: number } | null>(null);
  const [activeIndex, setActiveIndex] = useState(0);
  const id = useId();
  const listRef = useRef<HTMLDivElement>(null);
  const itemRefs = useRef<Array<HTMLButtonElement | null>>([]);

  const enabledIndexes = items
    .map((it, i) => (it.disabled ? -1 : i))
    .filter((i) => i >= 0);

  useEffect(() => {
    if (!pos) return;
    function onDocClick(e: MouseEvent) {
      if (!listRef.current?.contains(e.target as Node)) setPos(null);
    }
    document.addEventListener("mousedown", onDocClick);
    return () => document.removeEventListener("mousedown", onDocClick);
  }, [pos]);

  useEffect(() => {
    if (pos) itemRefs.current[activeIndex]?.focus();
  }, [pos, activeIndex]);

  function open(x: number, y: number) {
    setActiveIndex(enabledIndexes[0] ?? 0);
    setPos({ x, y });
  }

  function move(dir: 1 | -1) {
    const p = enabledIndexes.indexOf(activeIndex);
    setActiveIndex(
      enabledIndexes[(p + dir + enabledIndexes.length) % enabledIndexes.length]!
    );
  }

  function onListKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      move(1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      move(-1);
    } else if (e.key === "Escape") {
      e.preventDefault();
      setPos(null);
    }
  }

  return (
    <div
      className={clsx("mk-context-menu-host", className)}
      onContextMenu={(e) => {
        e.preventDefault();
        open(e.clientX, e.clientY);
      }}
    >
      {children}
      {pos && (
        <div
          ref={listRef}
          id={id}
          role="menu"
          className="mk-context-menu"
          style={{ left: pos.x, top: pos.y }}
          onKeyDown={onListKeyDown}
        >
          {items.map((item, i) => (
            <button
              key={item.id}
              ref={(el) => {
                itemRefs.current[i] = el;
              }}
              type="button"
              role="menuitem"
              className="mk-menu__item"
              tabIndex={i === activeIndex ? 0 : -1}
              disabled={item.disabled}
              onClick={() => {
                item.onSelect();
                setPos(null);
              }}
            >
              {item.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
