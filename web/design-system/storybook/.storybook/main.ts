// Storybook 8.x configuration for @maktaba/design-system.
//
// Stories live next to their components (`components/<Name>/<Name>.stories.tsx`).
// The vite framework keeps the build small; the a11y addon enforces
// AA contrast on every story (Story 17.2 TC).
//
// Visual-regression diffs gate merges (Chromatic addon, run from CI).

import type { StorybookConfig } from "@storybook/react-vite";

const config: StorybookConfig = {
  stories: ["../../components/**/*.stories.@(ts|tsx)"],
  addons: [
    "@storybook/addon-essentials",
    "@storybook/addon-a11y",
    "@storybook/addon-themes",
    "@chromatic-com/storybook",
  ],
  framework: {
    name: "@storybook/react-vite",
    options: {},
  },
  docs: { autodocs: "tag" },
  typescript: {
    reactDocgen: "react-docgen-typescript",
    check: false,
  },
};

export default config;
