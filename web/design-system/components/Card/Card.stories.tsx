import type { Meta, StoryObj } from "@storybook/react";
import { Card } from "./Card";
import { Button } from "../Button/Button";

const meta: Meta<typeof Card> = {
  title: "Layout/Card",
  component: Card,
};
export default meta;

type Story = StoryObj<typeof Card>;

export const Basic: Story = {
  args: { header: "Now playing", children: "Card body content." },
};

export const WithFooter: Story = {
  args: {
    header: "Delete library?",
    children: "This removes all indexed transcripts.",
    footer: <Button variant="destructive">Delete</Button>,
  },
};

export const Interactive: Story = {
  args: {
    interactive: true,
    tabIndex: 0,
    role: "button",
    "aria-label": "Open video",
    children: "Focusable card — grows 4% on TV focus.",
  },
};

export const Scrollable: Story = {
  args: {
    scrollable: true,
    children: <div style={{ height: 400 }}>Tall content scrolls, never clipped.</div>,
  },
};
