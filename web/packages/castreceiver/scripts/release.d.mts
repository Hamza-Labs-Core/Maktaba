// Types for release.mjs (story 58.07), for the TypeScript tests that import it.
export interface LiveBuild {
  level: number;
  path: string;
  hash?: string;
  built?: string;
  files?: string[];
}
export declare function fetchLive(
  base: string,
  fetchImpl?: typeof fetch
): Promise<{ builds: LiveBuild[]; reason: string }>;
export declare function changed(
  liveBuilds: { level: number; hash?: string }[],
  freshBuilds: { level: number; hash?: string }[]
): boolean;
