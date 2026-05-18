import type { Meta, StoryObj } from "@storybook/react";
import { Checkbox } from "./Checkbox";
import { Radio, RadioGroup } from "./Radio";
import { Toggle } from "./Toggle";

// Story 17.2 — boolean form controls. Each keeps a real focusable native
// input (visually hidden, in the a11y tree); Toggle is role="switch",
// RadioGroup is a labelled fieldset.

const meta: Meta = { title: "Forms/Choice" };
export default meta;

export const CheckboxStory: StoryObj = {
  name: "Checkbox",
  render: () => <Checkbox label="I accept the terms" />,
};

export const RadioGroupStory: StoryObj = {
  name: "RadioGroup",
  render: () => (
    <RadioGroup legend="Transcription quality" name="q">
      <Radio name="q" value="fast" label="Fast" defaultChecked />
      <Radio name="q" value="balanced" label="Balanced" />
      <Radio name="q" value="accurate" label="Accurate" />
    </RadioGroup>
  ),
};

export const ToggleStory: StoryObj = {
  name: "Toggle (switch)",
  render: () => <Toggle label="Auto-scroll transcript" defaultChecked />,
};
