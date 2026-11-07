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
