import { useState } from "react";
import type { Meta, StoryObj } from "@storybook/react";
import { Modal } from "./Modal";
import { Button } from "../Button/Button";

// Story 17.2 TC: modal traps focus and closes on Esc. Open it and Tab —
// focus cycles within the dialog; Esc or the backdrop closes it.

const meta: Meta<typeof Modal> = { title: "Overlays/Modal", component: Modal };
export default meta;

type Story = StoryObj<typeof Modal>;

export const Default: Story = {
  render: () => {
    const [open, setOpen] = useState(false);
    return (
      <>
        <Button onClick={() => setOpen(true)}>Open modal</Button>
        <Modal
          open={open}
          onClose={() => setOpen(false)}
          title="Delete this video?"
          footer={
            <>
              <Button variant="secondary" onClick={() => setOpen(false)}>
                Cancel
              </Button>
              <Button variant="destructive" onClick={() => setOpen(false)}>
                Delete
              </Button>
            </>
          }
        >
          This permanently removes the video and its transcripts.
        </Modal>
      </>
    );
  },
};
