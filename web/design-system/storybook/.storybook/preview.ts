// Story 17.2 — Storybook preview. Defines the global decorators that
// implement the LTR + RTL and light + dark snapshots required by the
// AC. Direction is exposed as a toolbar global so a story can pin
// `parameters.direction = "rtl"` and the decorator applies it.

import type { Preview } from "@storybook/react";
import "@maktaba/design-system/dist/css/tokens.css";

const preview: Preview = {
  parameters: {
    controls: { matchers: { color: /(background|color)$/i, date: /Date$/i } },
    a11y: { config: { rules: [{ id: "color-contrast", enabled: true }] } },
  },

  globalTypes: {
    theme: {
      description: "Theme variant",
      defaultValue: "light",
      toolbar: {
        title: "Theme",
        items: [
          { value: "light", title: "Light" },
          { value: "dark", title: "Dark" },
          { value: "high-contrast", title: "High contrast" },
        ],
        dynamicTitle: true,
      },
    },
    direction: {
      description: "Reading direction",
      defaultValue: "ltr",
      toolbar: {
        title: "Direction",
        items: [
          { value: "ltr", title: "LTR" },
          { value: "rtl", title: "RTL (Arabic)" },
        ],
        dynamicTitle: true,
      },
    },
  },

  decorators: [
    (Story, ctx) => {
      const dir = ctx.parameters?.direction ?? ctx.globals?.direction ?? "ltr";
      const theme = ctx.parameters?.theme ?? ctx.globals?.theme ?? "light";
      // Apply via DOM rather than React props so the side-effects on
      // <html> match what consumer apps will see.
      if (typeof document !== "undefined") {
        document.documentElement.setAttribute("dir", dir);
        if (theme === "dark") {
          document.documentElement.setAttribute("data-theme", "dark");
        } else {
          document.documentElement.removeAttribute("data-theme");
        }
      }
      return Story();
    },
  ],
};

export default preview;
