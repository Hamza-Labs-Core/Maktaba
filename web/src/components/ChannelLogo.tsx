// A channel's logo beside its name in admin lists; a channel without one
// (the logo route answers 404) shows nothing.
import { useState } from "react";

export function ChannelLogo({ id }: { id: string }) {
  const [broken, setBroken] = useState(false);
  if (broken) return null;
  return (
    <img
      className="mkt-chan-logo"
      src={`/api/channels/${encodeURIComponent(id)}/logo`}
      alt=""
      loading="lazy"
      onError={() => setBroken(true)}
    />
  );
}
