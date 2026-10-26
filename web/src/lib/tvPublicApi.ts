// Epic 34 (first slice): the public TV site, admin only.
//
//   GET  /api/tv/public                   PublicView
//   PUT  /api/tv/public                   {enabled, site_url} -> PublicView | 400 invalid_site_url
//   PUT  /api/tv/public/channels/{id}     PublicChange -> PublicChannel
//                                         422 rights_confirmation_required | 409 slug_taken | 400 invalid_slug
//   POST /api/tv/public/site-secrets      SiteSecrets (shown once)
import { api } from "./api";

export interface PublicSettings {
  enabled: boolean;
  site_url: string;
}

export interface PublicChannel {
  channel_id: string;
  number: number;
  name: string;
  enabled: boolean;
  public: boolean;
  slug: string;
  rights_confirmed_by: string | null;
  rights_confirmed_at: string | null;
  rights_note: string;
  on_air: boolean;
}

export interface PublishStatus {
  last_ok_at: string | null;
  last_error?: string;
  last_at: string | null;
}

export interface PublicView {
  settings: PublicSettings;
  channels: PublicChannel[] | null;
  secrets_set: boolean;
  publisher: PublishStatus | null;
}

export interface PublicChange {
  public?: boolean;
  slug?: string;
  rights_note?: string;
  rights_confirmed?: boolean;
}

export interface SiteSecrets {
  home_hmac: string;
  signing_key_id: string;
  signing_key_jwk: string;
}

const enc = encodeURIComponent;

export const tvPublicApi = {
  status: () => api.get<PublicView>("/api/tv/public"),
  save: (s: PublicSettings) => api.put<PublicView>("/api/tv/public", s),
  setChannel: (id: string, c: PublicChange) =>
    api.put<PublicChannel>(`/api/tv/public/channels/${enc(id)}`, c),
  newSecrets: () => api.post<SiteSecrets>("/api/tv/public/site-secrets"),
};
