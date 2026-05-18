import { useState } from "react";
import { clsx } from "../internal/clsx";
import "./avatar.css";

// Story 17.2 — Avatar. Renders an image when `src` is given and loads
// cleanly; otherwise falls back to initials derived from `name`. The
// rendered element carries an accessible name (img alt or aria-label).

export interface AvatarProps {
  name: string;
  src?: string;
  size?: "sm" | "md" | "lg";
  className?: string;
}

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  if (parts.length === 1) return parts[0]!.slice(0, 2).toUpperCase();
  return (parts[0]![0]! + parts[parts.length - 1]![0]!).toUpperCase();
}

export function Avatar({ name, src, size = "md", className }: AvatarProps) {
  const [failed, setFailed] = useState(false);
  const showImg = src && !failed;
  return (
    <span
      className={clsx("mk-avatar", `mk-avatar--${size}`, className)}
      role="img"
      aria-label={name}
    >
      {showImg ? (
        <img
          className="mk-avatar__img"
          src={src}
          alt=""
          onError={() => setFailed(true)}
        />
      ) : (
        <span className="mk-avatar__initials" aria-hidden="true">
          {initials(name)}
        </span>
      )}
    </span>
  );
}
