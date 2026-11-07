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
      "dist-cloud",
      "dist-admin",
      "node_modules",
      "mockups",
      "wiki-app",
      "packages/devportal/dist",
      "packages/devportal/node_modules",
      "packages/devportal/.stage",
      "packages/devportal/.wrangler",
      "design-system/build",
      "design-system/storybook",
      "design-system/**/*.stories.tsx",
    ],
  },
  {
    files: [
      "src/**/*.{js,ts,tsx}",
      "cloud/**/*.{js,ts,tsx}",
      "admin/**/*.{js,ts,tsx}",
      "design-system/**/*.{js,ts,tsx}",
      "packages/**/*.{js,ts,tsx}",
    ],
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
  },
  {
    // story 50.32: the user app never imports the operator console (web/admin), so no admin
    // code can reach a user's browser; tools/lint/no-admin-in-user-bundle.mjs checks the build
    files: ["cloud/**/*.{js,ts,tsx}"],
    rules: {
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: ["**/admin/src/**", "**/admin/src"],
              message: "The user app never imports the operator console.",
            },
          ],
        },
      ],
    },
  }
);
