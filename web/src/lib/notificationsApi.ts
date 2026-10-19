// The notification centre (Epic 33, story 33.13): a general in-app store, TV changes its
// first user.
//
//   GET  /api/notifications?limit=&unread=1   {items, unread}
//   POST /api/notifications/{id}/read         {unread}
//   POST /api/notifications/read-all          {unread}
//
// A notice carries a message key and params; the web app localises it (en / ar).
import { api } from "./api";

export interface AppNotification {
  id: string;
  kind: string;
  severity: "info" | "success" | "warn" | "error";
  title_key: string;
  params: Record<string, unknown>;
  link: string;
  audience: string;
  created_at: string;
  read: boolean;
}

export interface NotificationList {
  items: AppNotification[];
  unread: number;
}

export const notificationsApi = {
  list: (limit = 30) => api.get<NotificationList>(`/api/notifications?limit=${limit}`),
  read: (id: string) =>
    api.post<{ unread: number }>(`/api/notifications/${encodeURIComponent(id)}/read`),
  readAll: () => api.post<{ unread: number }>(`/api/notifications/read-all`),
};
