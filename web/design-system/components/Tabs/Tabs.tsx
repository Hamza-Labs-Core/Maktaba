import { useId, useRef, useState } from "react";
import type { KeyboardEvent, ReactNode } from "react";
import { clsx } from "../internal/clsx";
import "./tabs.css";

// Story 17.2 — Tabs. ARIA tablist pattern: roving tabindex, Left/Right
// (Home/End) arrow navigation, `aria-selected` + `aria-controls` wiring.
// Controlled or uncontrolled via `defaultValue` / `value`+`onChange`.

export interface TabItem {
  id: string;
  label: ReactNode;
  content: ReactNode;
}

export interface TabsProps {
  items: TabItem[];
  defaultValue?: string;
  value?: string;
  onChange?: (id: string) => void;
  /** Accessible name for the tablist. */
  label: string;
  className?: string;
}

export function Tabs({
  items,
  defaultValue,
  value,
  onChange,
  label,
  className,
}: TabsProps) {
  const baseId = useId();
  const [internal, setInternal] = useState(defaultValue ?? items[0]?.id ?? "");
  const active = value ?? internal;
  const tabsRef = useRef<Array<HTMLButtonElement | null>>([]);

  function select(id: string) {
    if (value === undefined) setInternal(id);
    onChange?.(id);
  }

  function onKeyDown(e: KeyboardEvent<HTMLButtonElement>, index: number) {
    let next = index;
    if (e.key === "ArrowRight") next = (index + 1) % items.length;
    else if (e.key === "ArrowLeft") next = (index - 1 + items.length) % items.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = items.length - 1;
    else return;
    e.preventDefault();
    const target = items[next]!;
    select(target.id);
    tabsRef.current[next]?.focus();
  }

  return (
    <div className={clsx("mk-tabs", className)}>
      <div className="mk-tabs__list" role="tablist" aria-label={label}>
        {items.map((item, i) => {
          const selected = item.id === active;
          return (
            <button
              key={item.id}
              ref={(el) => {
                tabsRef.current[i] = el;
              }}
              type="button"
              role="tab"
              id={`${baseId}-tab-${item.id}`}
              aria-selected={selected}
              aria-controls={`${baseId}-panel-${item.id}`}
              tabIndex={selected ? 0 : -1}
              className={clsx("mk-tabs__tab", selected && "mk-tabs__tab--active")}
              onClick={() => select(item.id)}
              onKeyDown={(e) => onKeyDown(e, i)}
            >
              {item.label}
            </button>
          );
        })}
      </div>
      {items.map((item) => (
        <div
          key={item.id}
          role="tabpanel"
          id={`${baseId}-panel-${item.id}`}
          aria-labelledby={`${baseId}-tab-${item.id}`}
          hidden={item.id !== active}
          tabIndex={0}
          className="mk-tabs__panel"
        >
          {item.id === active && item.content}
        </div>
      ))}
    </div>
  );
}
