import { describe, it, expect } from "vitest";
import { fireEvent, render } from "@testing-library/react";
import { ChannelLogo } from "./ChannelLogo";

describe("ChannelLogo", () => {
  it("shows the channel's logo and hides when it has none", () => {
    const { container } = render(<ChannelLogo id="c 1" />);
    const img = container.querySelector("img")!;
    expect(img).toHaveAttribute("src", "/api/channels/c%201/logo");
    fireEvent.error(img);
    expect(container.querySelector("img")).toBeNull();
  });
});
