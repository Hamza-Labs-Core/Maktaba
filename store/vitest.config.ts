import { defineConfig } from "vitest/config";

export default defineConfig({
  test: { include: ["signing/test/**/*.test.ts", "test/**/*.test.ts"] },
});
