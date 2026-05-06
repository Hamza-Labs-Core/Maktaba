# API catalog

All 70 REST/WebSocket endpoints in
[`shared/api/openapi.yaml`](../../shared/api/openapi.yaml). Each row links the
endpoint to the stories that mention it and to a candidate mockup that shows
the UI for that surface.

| Method | Path | Tag | Summary | Owning stories |
|--------|------|-----|---------|----------------|
| `POST` | `/auth/login` | Auth | Authenticate with username and password | [7.19](stories-map.md), [10.2](stories-map.md), [10.3](stories-map.md), [10.12](stories-map.md), [11.10](stories-map.md) *(+1)* |
| `POST` | `/auth/logout` | Auth | Revoke current credentials | [10.5](stories-map.md), [11.14](stories-map.md) |
| `GET` | `/auth/me` | Auth | Return the authenticated user | — |
| `POST` | `/auth/refresh` | Auth | Exchange a refresh token for a fresh access token | [10.4](stories-map.md), [10.12](stories-map.md), [11.10](stories-map.md), [23.6](stories-map.md) |
| `POST` | `/auth/register` | Auth | Create a new user account | — |
| `GET` | `/collections` | Collections | List collections | [9.14](stories-map.md) |
| `POST` | `/collections` | Collections | Create a collection (manual or smart) | [9.14](stories-map.md) |
| `DELETE` | `/collections/{id}` | Collections | Delete a collection | [9.14](stories-map.md) |
| `GET` | `/collections/{id}` | Collections | Fetch a collection | [9.14](stories-map.md) |
| `PATCH` | `/collections/{id}` | Collections | Update a collection | [9.14](stories-map.md) |
| `GET` | `/collections/{id}/videos` | Collections | List videos in a collection | — |
| `POST` | `/collections/{id}/videos` | Collections | Add a video to a collection | — |
| `DELETE` | `/collections/{id}/videos/{videoId}` | Collections | Remove a video from a collection | — |
| `GET` | `/devices` | Devices | List the current user's registered devices | [7.22](stories-map.md), [11.10](stories-map.md), [12.4](stories-map.md), [12.10](stories-map.md), [16.7](stories-map.md) |
| `POST` | `/devices/register` | Devices | Register or rotate a push-notification device | [7.22](stories-map.md), [11.10](stories-map.md), [12.4](stories-map.md), [12.10](stories-map.md) |
| `DELETE` | `/devices/{id}` | Devices | Revoke a registered device | [7.22](stories-map.md), [12.10](stories-map.md) |
| `GET` | `/jobs` | Jobs | List processing jobs | [3.7](stories-map.md), [6.4](stories-map.md), [6.5](stories-map.md), [7.3](stories-map.md), [7.12](stories-map.md) *(+4)* |
| `GET` | `/jobs/{id}` | Jobs | Fetch a job | [3.7](stories-map.md), [6.4](stories-map.md), [6.5](stories-map.md), [7.12](stories-map.md), [11.5](stories-map.md) *(+1)* |
| `POST` | `/jobs/{id}/cancel` | Jobs | Cancel a job | [6.4](stories-map.md), [7.12](stories-map.md) |
| `POST` | `/jobs/{id}/pause` | Jobs | Request a graceful pause | [3.7](stories-map.md), [6.4](stories-map.md), [7.12](stories-map.md) |
| `POST` | `/jobs/{id}/resume` | Jobs | Make a paused job re-claimable | [3.7](stories-map.md), [6.4](stories-map.md), [7.12](stories-map.md) |
| `POST` | `/jobs/{id}/retry` | Jobs | Reset a failed job to `pending` | [6.5](stories-map.md), [7.12](stories-map.md) |
| `POST` | `/videos/{id}/cancel` | Jobs | Cancel every active job for this video | — |
| `POST` | `/videos/{id}/pause` | Jobs | Pause every active job for this video | [7.12](stories-map.md) |
| `POST` | `/videos/{id}/resume` | Jobs | Resume every paused job for this video | — |
| `GET` | `/libraries` | Libraries | List libraries | [1.1](stories-map.md), [1.4](stories-map.md), [7.3](stories-map.md), [9.3](stories-map.md), [9.6](stories-map.md) *(+11)* |
| `POST` | `/libraries` | Libraries | Create a library | [1.1](stories-map.md), [1.4](stories-map.md), [7.3](stories-map.md), [9.3](stories-map.md), [9.6](stories-map.md) *(+11)* |
| `DELETE` | `/libraries/{id}` | Libraries | Delete a library | [1.1](stories-map.md), [1.4](stories-map.md), [7.3](stories-map.md), [9.3](stories-map.md), [9.6](stories-map.md) *(+7)* |
| `GET` | `/libraries/{id}` | Libraries | Fetch a library | [1.1](stories-map.md), [1.4](stories-map.md), [7.3](stories-map.md), [9.3](stories-map.md), [9.6](stories-map.md) *(+7)* |
| `PATCH` | `/libraries/{id}` | Libraries | Update a library | [1.1](stories-map.md), [1.4](stories-map.md), [7.3](stories-map.md), [9.3](stories-map.md), [9.6](stories-map.md) *(+7)* |
| `POST` | `/libraries/{id}/scan` | Libraries | Enqueue a scan job for a library | [1.1](stories-map.md), [1.4](stories-map.md), [7.3](stories-map.md), [9.3](stories-map.md), [9.6](stories-map.md) |
| `GET` | `/libraries/{id}/stats` | Libraries | Aggregate stats for a library | [7.3](stories-map.md), [9.7](stories-map.md) |
| `GET` | `/queue/stats` | Queue | Aggregate queue stats per stage | [6.9](stories-map.md), [7.12](stories-map.md), [7.13](stories-map.md), [11.5](stories-map.md), [18.7](stories-map.md) *(+1)* |
| `GET` | `/recommendations` | Recommendations | Personalized video recommendations | [7.21](stories-map.md), [14.6](stories-map.md), [14.7](stories-map.md) |
| `GET` | `/search` | Search | Hybrid search across the library | [5.6](stories-map.md), [7.8](stories-map.md), [7.9](stories-map.md), [7.19](stories-map.md), [11.4](stories-map.md) *(+5)* |
| `POST` | `/search` | Search | Hybrid search with a structured request body | [5.6](stories-map.md), [7.8](stories-map.md), [7.9](stories-map.md), [7.19](stories-map.md), [11.4](stories-map.md) *(+5)* |
| `POST` | `/search/save` | Search | Save a search query for the current user | [7.9](stories-map.md), [11.4](stories-map.md), [11.10](stories-map.md) |
| `GET` | `/search/saved` | Search | List the current user's saved searches | [7.9](stories-map.md) |
| `DELETE` | `/search/saved/{id}` | Search | Delete a saved search | — |
| `GET` | `/search/suggest` | Search | Autocomplete suggestions | [5.6](stories-map.md), [7.8](stories-map.md), [11.4](stories-map.md), [14.2](stories-map.md), [14.4](stories-map.md) |
| `POST` | `/sessions` | Sessions | Mint a streaming session | [7.10](stories-map.md), [7.11](stories-map.md), [8.1](stories-map.md), [8.3](stories-map.md), [8.9](stories-map.md) *(+6)* |
| `GET` | `/sessions/capabilities` | Sessions | Streaming server capabilities | — |
| `DELETE` | `/sessions/{id}` | Sessions | Close a streaming session | [7.10](stories-map.md), [7.11](stories-map.md), [11.3](stories-map.md), [11.10](stories-map.md), [11.14](stories-map.md) *(+1)* |
| `GET` | `/sessions/{id}` | Sessions | Fetch a session | [7.10](stories-map.md), [7.11](stories-map.md), [11.3](stories-map.md), [11.10](stories-map.md), [11.14](stories-map.md) *(+1)* |
| `POST` | `/sessions/{id}/progress` | Sessions | Heartbeat session progress | [7.11](stories-map.md), [11.3](stories-map.md), [11.10](stories-map.md), [12.3](stories-map.md) |
| `GET` | `/settings` | Settings | Read effective settings | [7.15](stories-map.md), [10.14](stories-map.md), [11.6](stories-map.md), [12.9](stories-map.md), [16.4](stories-map.md) *(+2)* |
| `PUT` | `/settings` | Settings | Replace the DB-stored settings layer | [7.15](stories-map.md), [10.14](stories-map.md), [11.6](stories-map.md), [12.9](stories-map.md), [16.4](stories-map.md) *(+2)* |
| `GET` | `/settings/stt-backends` | Settings | Enumerate available STT backends | [7.15](stories-map.md), [11.6](stories-map.md) |
| `POST` | `/settings/stt-test` | Settings | Dry-run a transcribe request against a backend | [7.15](stories-map.md), [11.6](stories-map.md) |
| `GET` | `/speakers` | Speakers | List speakers | [7.14](stories-map.md), [9.11](stories-map.md) |
| `POST` | `/speakers/merge` | Speakers | Merge two speaker labels into one | [7.14](stories-map.md), [9.11](stories-map.md) |
| `PATCH` | `/speakers/{id}` | Speakers | Rename a speaker | — |
| `GET` | `/system/health` | System | Liveness / readiness check | [3.1](stories-map.md), [7.20](stories-map.md), [10.15](stories-map.md), [19.7](stories-map.md), [21.4](stories-map.md) |
| `GET` | `/system/metrics` | System | Prometheus exposition (text format) | — |
| `GET` | `/system/version` | System | Build identification | [7.20](stories-map.md), [12.2](stories-map.md), [22.5](stories-map.md) |
| `GET` | `/tags` | Tags | List tags | [7.14](stories-map.md) |
| `POST` | `/tags` | Tags | Create a tag | [7.14](stories-map.md) |
| `PATCH` | `/videos/{id}/tags` | Tags | Add or remove tags on a video | [7.14](stories-map.md) |
| `GET` | `/videos` | Videos | List videos | [7.4](stories-map.md), [7.5](stories-map.md), [7.6](stories-map.md), [7.7](stories-map.md), [7.12](stories-map.md) *(+10)* |
| `DELETE` | `/videos/{id}` | Videos | Delete a video | [7.4](stories-map.md), [7.5](stories-map.md), [7.6](stories-map.md), [7.7](stories-map.md), [7.12](stories-map.md) *(+7)* |
| `GET` | `/videos/{id}` | Videos | Fetch a video with full metadata | [7.4](stories-map.md), [7.5](stories-map.md), [7.6](stories-map.md), [7.7](stories-map.md), [7.12](stories-map.md) *(+7)* |
| `PATCH` | `/videos/{id}` | Videos | Update mutable video fields | [7.4](stories-map.md), [7.5](stories-map.md), [7.6](stories-map.md), [7.7](stories-map.md), [7.12](stories-map.md) *(+7)* |
| `GET` | `/videos/{id}/chapters` | Videos | List chapters for a video (ordered by `seq`) | [7.7](stories-map.md), [9.18](stories-map.md) |
| `POST` | `/videos/{id}/process` | Videos | Enqueue a single processing stage for a video | [7.5](stories-map.md) |
| `POST` | `/videos/{id}/reprocess` | Videos | Reset state and reprocess from a given stage | [7.5](stories-map.md) |
| `GET` | `/videos/{id}/segments` | Videos | Read a transcript window for a video | [7.6](stories-map.md) |
| `GET` | `/videos/{id}/subtitles` | Videos | List subtitle tracks (signed URLs) | [7.7](stories-map.md), [8.11](stories-map.md) |
| `GET` | `/videos/{id}/progress` | Watch progress | Fetch playback state for the current user and a video | — |
| `PUT` | `/videos/{id}/progress` | Watch progress | Upsert playback state for the current user and a video | — |
| `GET` | `/ws` | WebSocket | Upgrade to the realtime event stream | [1.1](stories-map.md), [7.11](stories-map.md), [7.16](stories-map.md), [10.15](stories-map.md), [11.2](stories-map.md) *(+2)* |
