// Flat-config ESLint setup. Story 22.1 ships a minimal config; Epic 11
// will extend it with React/Next-specific rules.
//
// Adds typescript-eslint so the parser can handle TS + JSX syntax in
// src/**/*.tsx; without it, eslint chokes on `<Foo />`, `interface`,
// and other TS-only tokens with "Unexpected token" errors.

import tseslint from "typescript-eslint";

export default tseslint.config(
  {
    ignores: [
      "dist",
      "node_modules",
      "mockups",
      "wiki-app",
      "design-system/build",
      "design-system/storybook",
      "design-system/**/*.stories.tsx",
    ],
  },
  {
    files: ["src/**/*.{js,ts,tsx}", "design-system/**/*.{js,ts,tsx}"],
    languageOptions: {
      parser: tseslint.parser,
      ecmaVersion: "latest",
      sourceType: "module",
      parserOptions: {
        ecmaFeatures: { jsx: true },
      },
    },
    rules: {
      // The base `no-unused-vars` flags TS-only constructs (type-only
      // imports, ambient declarations) as unused. typescript-eslint's
      // version understands those.
      "no-unused-vars": "off",
      "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_" }],
      "no-undef": "off", // tsc handles undefined-identifier checking
    },
    plugins: {
      "@typescript-eslint": tseslint.plugin,
    },
  }
);
