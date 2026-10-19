// GET /api/cluster/files — the machines' folders on the share (by IP), the
// processes that wrote there and the days on record per kind. Shared by
// the Logs, Queues and Library tabs.
import { useCallback, useEffect, useRef, useState } from "react";
import { clusterChannelApi, type ClusterFiles, type FileNode } from "../../lib/clusterApi";

export interface FilesState {
  files: ClusterFiles | null;
  error: boolean;
  reload: () => void;
}

function parse(v: unknown): ClusterFiles {
  const o = (v && typeof v === "object" ? v : {}) as Partial<ClusterFiles>;
  const nodes = Array.isArray(o.nodes) ? o.nodes : [];
  return {
    files: o.files && typeof o.files === "object" ? o.files : null,
    nodes: nodes
      .filter((n): n is FileNode => !!n && typeof (n as FileNode).ip === "string")
      .map((n) => ({
        ip: n.ip,
        processes: Array.isArray(n.processes) ? n.processes : [],
        dates: n.dates && typeof n.dates === "object" ? n.dates : {},
      })),
  };
}

export function useClusterFiles(): FilesState {
  const [files, setFiles] = useState<ClusterFiles | null>(null);
  const [error, setError] = useState(false);
  const alive = useRef(true);
  const load = useCallback(() => {
    clusterChannelApi
      .files()
      .then((v) => {
        if (!alive.current) return;
        setFiles(parse(v));
        setError(false);
      })
      .catch(() => {
        if (alive.current) setError(true);
      });
  }, []);
  useEffect(() => {
    alive.current = true;
    load();
    return () => {
      alive.current = false;
    };
  }, [load]);
  return { files, error, reload: load };
}

/** Days on record of one kind across all machines, newest first. */
export function datesOf(files: ClusterFiles | null, kind: keyof FileNode["dates"]): string[] {
  const set = new Set<string>();
  for (const n of files?.nodes ?? []) for (const d of n.dates[kind] ?? []) set.add(d);
  return [...set].sort().reverse();
}

/** The first machine folder holding `kind` for `date` (the home writes queues and library). */
export function nodeWith(
  files: ClusterFiles | null,
  kind: keyof FileNode["dates"],
  date: string
): FileNode | null {
  return files?.nodes.find((n) => (n.dates[kind] ?? []).includes(date)) ?? null;
}
