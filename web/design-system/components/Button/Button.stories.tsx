import type { Meta, StoryObj } from "@storybook/react";
import { Button } from "./Button";

// Storybook contract for Button (Story 17.2 AC). Each variant × size is
// a story; LTR + RTL and light + dark snapshots are produced via
// parameters.themes / parameters.direction (configured in
// .storybook/preview.ts).

const meta: Meta<typeof Button> = {
  title: "Foundations/Button",
  component: Button,
  parameters: {
    a11y: { config: { rules: [{ id: "color-contrast", enabled: true }] } },
  },
  args: { children: "Continue" },
};
export default meta;

type Story = StoryObj<typeof Button>;

export const Primary: Story = { args: { variant: "primary", size: "md" } };
export const Secondary: Story = { args: { variant: "secondary", size: "md" } };
export const Ghost: Story = { args: { variant: "ghost", size: "md" } };
export const Destructive: Story = {
  args: { variant: "destructive", size: "md", children: "Delete" },
};
export const Link: Story = { args: { variant: "link", size: "md", children: "Read more" } };

export const Small: Story = { args: { variant: "primary", size: "sm" } };
export const Large: Story = { args: { variant: "primary", size: "lg" } };

export const Loading: Story = {
  args: { variant: "primary", loading: true, children: "Save" },
  parameters: {
    docs: { description: { story: "Loading state preserves label width and disables click." } },
  },
};

export const Disabled: Story = { args: { variant: "primary", disabled: true } };

// RTL + Arabic copy snapshot — stories.preview applies dir="rtl" and
// font-family swap when "Direction" is set to "rtl" in the toolbar.
export const ArabicRTL: Story = {
  args: { variant: "primary", children: "متابعة" },
  parameters: { direction: "rtl" },
};
