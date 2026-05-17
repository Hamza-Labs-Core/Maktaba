// Vitest global setup: registers @testing-library/jest-dom matchers and
// auto-cleans the React tree between tests.
import "@testing-library/jest-dom/vitest";
import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

afterEach(() => {
  cleanup();
});
