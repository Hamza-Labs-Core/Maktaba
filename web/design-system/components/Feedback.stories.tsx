import { useState } from "react";
import type { Meta, StoryObj } from "@storybook/react";
import { Badge } from "./Badge/Badge";
import { Chip } from "./Chip/Chip";
import { Avatar } from "./Avatar/Avatar";
import { Skeleton } from "./Skeleton/Skeleton";
import { ProgressBar } from "./ProgressBar/ProgressBar";
import { EmptyState } from "./EmptyState/EmptyState";
import { ErrorState } from "./ErrorState/ErrorState";
import { Pagination } from "./Pagination/Pagination";
import { Tabs } from "./Tabs/Tabs";
import { Button } from "./Button/Button";

// Grouped gallery of the status / feedback / data-display primitives.
// Every entry below has its own a11y contract documented in the source.

const meta: Meta = { title: "Feedback/Gallery" };
export default meta;

export const Badges: StoryObj = {
  render: () => (
    <div style={{ display: "flex", gap: 8 }}>
      <Badge tone="neutral">Queued</Badge>
      <Badge tone="accent">Indexing</Badge>
      <Badge tone="success">Ready</Badge>
      <Badge tone="warn">Stale</Badge>
      <Badge tone="error">Failed</Badge>
    </div>
  ),
};

export const Chips: StoryObj = {
  render: () => (
    <div style={{ display: "flex", gap: 8 }}>
      <Chip>Arabic</Chip>
      <Chip selected>English</Chip>
      <Chip onRemove={() => {}}>Removable</Chip>
    </div>
  ),
};

export const Avatars: StoryObj = {
  render: () => (
    <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
      <Avatar name="Mahmoud Darwish" size="sm" />
      <Avatar name="Test User" size="md" />
      <Avatar name="Q" size="lg" />
    </div>
  ),
};

export const Skeletons: StoryObj = {
  render: () => (
    <div style={{ maxWidth: 280 }}>
      <Skeleton variant="rect" height={120} />
      <Skeleton variant="text" lines={3} />
    </div>
  ),
};

export const Progress: StoryObj = {
  render: () => (
    <div style={{ display: "grid", gap: 12, maxWidth: 320 }}>
      <ProgressBar value={64} label="Transcribing" />
      <ProgressBar indeterminate label="Connecting" />
    </div>
  ),
};

export const Empty: StoryObj = {
  render: () => (
    <EmptyState
      kind="filtered_out"
      title="No results"
      description="Try a broader query or clear filters."
      action={<Button variant="secondary">Clear filters</Button>}
    />
  ),
};

export const Error: StoryObj = {
  render: () => (
    <ErrorState
      kind="network"
      title="You're offline"
      description="Check your connection and try again."
      action={<Button>Retry</Button>}
    />
  ),
};

export const PaginationStory: StoryObj = {
  name: "Pagination",
  render: () => {
    const [page, setPage] = useState(3);
    return <Pagination page={page} pageCount={12} onChange={setPage} />;
  },
};

export const TabsStory: StoryObj = {
  name: "Tabs",
  render: () => (
    <Tabs
      label="Video details"
      items={[
        { id: "info", label: "Info", content: <p>Metadata</p> },
        { id: "transcript", label: "Transcript", content: <p>Transcript</p> },
        { id: "chapters", label: "Chapters", content: <p>Chapters</p> },
      ]}
    />
  ),
};
