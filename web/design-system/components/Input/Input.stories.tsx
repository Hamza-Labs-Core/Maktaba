import type { Meta, StoryObj } from "@storybook/react";
import { Input } from "./Input";

// Story 17.2 contract for Input. a11y: the label is programmatically
// associated; description/error are wired via aria-describedby and the
// error has role="alert". LTR + RTL via the toolbar global.

const meta: Meta<typeof Input> = {
  title: "Forms/Input",
  component: Input,
  parameters: {
    a11y: { config: { rules: [{ id: "label", enabled: true }] } },
  },
  args: { label: "Email", placeholder: "you@example.com" },
};
export default meta;

type Story = StoryObj<typeof Input>;

export const Default: Story = {};
export const WithDescription: Story = {
  args: { description: "We never share your email." },
};
export const Invalid: Story = {
  args: { error: "Enter a valid email address.", defaultValue: "nope" },
};
export const Disabled: Story = { args: { disabled: true } };
export const ArabicRTL: Story = {
  args: { label: "البريد الإلكتروني" },
  parameters: { direction: "rtl" },
};
