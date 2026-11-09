// Test helpers for the operator console: the cloud apps' fetch stub and fixtures, and a
// renderer that mounts the console's providers and routes at a given URL.
import { render } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { AppRoutes, Providers } from "../App";

export * from "@cloud-shared/test/fetch";

export function renderAt(path: string) {
  return render(
    <Providers>
      <MemoryRouter initialEntries={[path]}>
        <AppRoutes />
      </MemoryRouter>
    </Providers>
  );
}

/** A staff refresh route that restores a session for a staff account with these roles. */
export function staffSession(
  roles: string[] = ["service_admin"],
  email = "ops@example.com"
): [number, unknown] {
  return [
    200,
    {
      access_token: "staff-tok",
      expires_in: 600,
      idle_ends_at: "2026-11-08T12:15:00Z",
      capped_at: "2026-11-08T20:00:00Z",
      staff: { id: "s1", email, roles },
    },
  ];
}

/** A staff refresh route with no session (no cookie). */
export const staffSignedOut: [number, unknown] = [401, { error: "no_session" }];
