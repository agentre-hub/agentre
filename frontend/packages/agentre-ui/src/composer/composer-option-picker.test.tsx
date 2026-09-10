import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ComposerOptionPicker } from "./composer-option-picker";

describe("ComposerOptionPicker", () => {
  it("Given runtime options, When the user chooses one, Then the host receives its stable value", () => {
    const onChange = vi.fn();
    render(
      <ComposerOptionPicker
        ariaLabel="Permission mode"
        value="default"
        options={[
          { label: "Default", value: "default" },
          { label: "Accept edits", value: "acceptEdits" },
        ]}
        onChange={onChange}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Permission mode" }));
    fireEvent.click(screen.getByRole("option", { name: "Accept edits" }));
    expect(onChange).toHaveBeenCalledWith("acceptEdits");
  });
});
