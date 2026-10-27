// Whether the browser has a network, following its online / offline events.
import { useEffect, useState } from "react";
import { isOnline } from "./errorText";

export function useOnline(): boolean {
  const [online, setOnline] = useState(isOnline);
  useEffect(() => {
    const sync = () => setOnline(isOnline());
    window.addEventListener("online", sync);
    window.addEventListener("offline", sync);
    sync();
    return () => {
      window.removeEventListener("online", sync);
      window.removeEventListener("offline", sync);
    };
  }, []);
  return online;
}
