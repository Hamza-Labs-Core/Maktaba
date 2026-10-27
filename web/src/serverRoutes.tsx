// Epic 42.5: the server routes (processing, transcription, settings, the admin
// pages), each its own lazily loaded chunk. App.tsx mounts them only when the
// edition has them (lib/edition SERVER_PAGES), so a player build drops this
// module and every page it loads. The `@__PURE__` marks let Rollup drop the
// lazy() calls when nothing uses them; scripts/check-player-bundle.mjs checks.
import { lazy, type ComponentType } from "react";
import { Navigate, Route } from "react-router-dom";

// lazy() wants a default export; the pages export by name.
function page<K extends string>(load: () => Promise<Record<K, ComponentType>>, name: K) {
  return lazy(() => load().then((m) => ({ default: m[name] })));
}

const QueueOverview = /* @__PURE__ */ page(
  () => import("./pages/queue/QueueOverview"),
  "QueueOverview"
);
const QueueDetail = /* @__PURE__ */ page(() => import("./pages/queue/QueueDetail"), "QueueDetail");
const ItemHistory = /* @__PURE__ */ page(() => import("./pages/queue/ItemHistory"), "ItemHistory");
const Reports = /* @__PURE__ */ page(() => import("./pages/queue/Reports"), "Reports");
const Failures = /* @__PURE__ */ page(() => import("./pages/queue/Failures"), "Failures");
const Transcription = /* @__PURE__ */ page(
  () => import("./pages/transcription/Transcription"),
  "Transcription"
);
const Settings = /* @__PURE__ */ page(() => import("./pages/Settings"), "Settings");
const ServerStatus = /* @__PURE__ */ page(
  () => import("./pages/Cloud/ServerStatus"),
  "ServerStatus"
);
const AdminLibraries = /* @__PURE__ */ page(
  () => import("./pages/Admin/Libraries"),
  "AdminLibraries"
);
const AdminUsers = /* @__PURE__ */ page(() => import("./pages/Admin/Users"), "AdminUsers");
const LibraryACL = /* @__PURE__ */ page(() => import("./pages/Admin/LibraryACL"), "LibraryACL");
const AdminExtensions = /* @__PURE__ */ page(
  () => import("./pages/Admin/Extensions"),
  "AdminExtensions"
);
const AdminEnrichment = /* @__PURE__ */ page(
  () => import("./pages/Admin/Enrichment"),
  "AdminEnrichment"
);
const Channels = /* @__PURE__ */ page(() => import("./pages/Admin/Channels"), "Channels");
const FillerPools = /* @__PURE__ */ page(() => import("./pages/Admin/FillerPools"), "FillerPools");
const AdminLiveTV = /* @__PURE__ */ page(() => import("./pages/Admin/LiveTV"), "AdminLiveTV");
const AdminTvChanges = /* @__PURE__ */ page(
  () => import("./pages/Admin/LiveTV/Changes"),
  "AdminTvChanges"
);
const AdminTvChangeDetail = /* @__PURE__ */ page(
  () => import("./pages/Admin/LiveTV/ChangeDetail"),
  "AdminTvChangeDetail"
);
const AdminAnalytics = /* @__PURE__ */ page(
  () => import("./pages/Admin/Analytics"),
  "AdminAnalytics"
);
const AdminAuditLog = /* @__PURE__ */ page(() => import("./pages/Admin/AuditLog"), "AdminAuditLog");
const SystemHealth = /* @__PURE__ */ page(
  () => import("./pages/Admin/SystemHealth"),
  "SystemHealth"
);
const Machines = /* @__PURE__ */ page(() => import("./pages/Admin/Machines"), "Machines");
const AdminUpdates = /* @__PURE__ */ page(() => import("./pages/Admin/Updates"), "AdminUpdates");
const AdminLogs = /* @__PURE__ */ page(() => import("./pages/Admin/Logs"), "AdminLogs");
const CloudDevices = /* @__PURE__ */ page(() => import("./pages/Cloud/Devices"), "CloudDevices");

/** The server routes, children of the signed-in shell (AppShell suspends them). */
export function serverRoutes() {
  return (
    <>
      <Route path="/queue" element={<QueueOverview />} />
      <Route path="/queue/items/:kind/:id" element={<ItemHistory />} />
      <Route path="/queue/reports" element={<Reports />} />
      <Route path="/queue/failures" element={<Failures />} />
      <Route path="/queue/:stage" element={<QueueDetail />} />
      <Route path="/transcription" element={<Transcription />} />
      <Route path="/settings/*" element={<Settings />} />
      <Route path="/billing" element={<Navigate to="/settings/subscription" replace />} />
      <Route path="/cloud/status" element={<ServerStatus />} />
      <Route path="/admin/libraries" element={<AdminLibraries />} />
      <Route path="/admin/users" element={<AdminUsers />} />
      <Route path="/admin/library-acl" element={<LibraryACL />} />
      <Route path="/admin/extensions" element={<AdminExtensions />} />
      <Route path="/admin/enrichment" element={<AdminEnrichment />} />
      <Route path="/admin/channels" element={<Channels />} />
      <Route path="/admin/filler" element={<FillerPools />} />
      <Route path="/admin/live-tv" element={<AdminLiveTV />} />
      <Route path="/admin/live-tv/changes" element={<AdminTvChanges />} />
      <Route path="/admin/live-tv/changes/:id" element={<AdminTvChangeDetail />} />
      <Route path="/admin/analytics" element={<AdminAnalytics />} />
      <Route path="/admin/audit" element={<AdminAuditLog />} />
      <Route path="/admin/health" element={<SystemHealth />} />
      <Route path="/admin/machines" element={<Machines />} />
      <Route path="/admin/updates" element={<AdminUpdates />} />
      <Route path="/admin/logs" element={<AdminLogs />} />
      <Route path="/admin/devices" element={<CloudDevices />} />
    </>
  );
}
