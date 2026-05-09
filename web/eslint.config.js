// Flat-config ESLint setup. Story 22.1 ships a minimal config; Epic 11
// will extend it with React/Next-specific rules.
export default [
  {
    ignores: ["dist", "node_modules", "mockups", "wiki-app"],
  },
  {
    files: ["src/**/*.{js,ts,tsx}"],
    languageOptions: {
      ecmaVersion: "latest",
      sourceType: "module",
    },
    rules: {
      "no-unused-vars": "error",
      "no-undef": "error",
    },
  },
];
