// Epic 51 (story 51.12): one machine's components. Admin-only on the server, as every
// components route (story 51.08): no sign-in is 401, anyone else 403.
//
//   GET  /api/components?machine=<node_id>                 MachineList
//   PUT  /api/components/{id}/machines/{node_id}           {want | null, generation} (null follows the home)
//   POST /api/components/{id}/machines/{node_id}/stop      held on that machine until start (no body)
//   POST /api/components/{id}/machines/{node_id}/start     clears that machine's hold
//
// Preview and apply are 51.10's calls with "machine" on each change.
import { api } from "./api";
import type { ApplyResult, ComponentView, Effect, Hold, Preview, Want } from "./componentsApi";

export interface Override {
  node: string;
  id: string;
  /** absent: the machine follows the home's want */
  want?: Want;
  hold?: Hold;
  generation: number;
}

export interface MachineState {
  /** what the machine is to do: its override's want, else the home's */
  want: Want;
  hold: Hold | null;
  generation: number;
  override: Override | null;
  phase: string;
  reason?: string;
  expected: Effect;
  /** whether the machine wrote a status row for it */
  reported: boolean;
  /** the machine has been silent longer than 90 s: its phase may be old */
  stale: boolean;
  updated_at?: string;
  /** its stages switched off on this machine (cluster_node_stages) */
  stages_off: string[];
  /** offered when the machine is approved as a worker */
  worker_offer: boolean;
  ram_mb?: number;
  gpu?: string;
  /** platform_unsupported or not_enough_memory */
  cannot_run?: string;
}

export interface MachineComponent extends ComponentView {
  here: MachineState;
}

export interface MachineInfo {
  id: string;
  name: string;
  state: string;
  platform?: string;
  memory_mb?: number;
  gpu: boolean;
  heard_at?: string;
  stale: boolean;
}

export interface MachineList {
  revision: number;
  machine: MachineInfo;
  components: MachineComponent[];
  worker_on: number;
  worker_offered: number;
}

export type MachineChange = {
  id: string;
  /** "follow" clears the override's want */
  want?: Want | "follow";
  hold?: boolean;
  generation?: number;
};

const path = (id: string, node: string) =>
  `/api/components/${encodeURIComponent(id)}/machines/${encodeURIComponent(node)}`;

export const machineComponentsApi = {
  list: (node: string) =>
    api.get<MachineList>(`/api/components?machine=${encodeURIComponent(node)}`),
  setWant: (id: string, node: string, want: Want | null, generation: number) =>
    api.put<MachineComponent>(path(id, node), { want, generation }),
  stop: (id: string, node: string) => api.post<MachineComponent>(`${path(id, node)}/stop`),
  start: (id: string, node: string) => api.post<MachineComponent>(`${path(id, node)}/start`),
  preview: (node: string, ch: MachineChange) =>
    api.post<Preview>("/api/components/preview", { changes: [{ ...ch, machine: node }] }),
  apply: (node: string, ch: MachineChange) =>
    api.post<ApplyResult>("/api/components/apply", { changes: [{ ...ch, machine: node }] }),
};

/** A list answer that is the machine list (an older home answers the plain list). */
export function isMachineList(v: unknown): v is MachineList {
  return (
    typeof v === "object" &&
    v !== null &&
    Array.isArray((v as MachineList).components) &&
    typeof (v as MachineList).machine === "object"
  );
}
