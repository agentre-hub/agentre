import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";

import { ChatComposer } from "./chat-composer";
import type { AIChatInputHandle } from "../chat-input";

describe("ChatComposer", () => {
  it("Given image input is supported, When a PNG is selected and submitted, Then the host receives text and the image data URL", async () => {
    const onSubmit = vi.fn();
    render(<ChatComposer supportsImageInput onSubmit={onSubmit} />);

    const file = new File([new Uint8Array([1, 2, 3])], "shot.png", {
      type: "image/png",
    });
    fireEvent.change(
      screen.getByLabelText("Add image", { selector: "input" }),
      {
        target: { files: [file] },
      },
    );

    await screen.findByAltText("shot.png");
    fireEvent.click(screen.getByRole("button", { name: "Send" }));

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith({
        images: [
          expect.objectContaining({
            dataUrl: expect.stringMatching(/^data:image\/png;base64,/),
            mediaType: "image/png",
            name: "shot.png",
          }),
        ],
        text: "",
      }),
    );
  });

  it("Given image input is unavailable, When rendered, Then the image affordance is absent", () => {
    render(<ChatComposer supportsImageInput={false} onSubmit={vi.fn()} />);
    expect(screen.queryByLabelText("Add image")).toBeNull();
  });

  it("Given a host input handle, When it restores a draft, Then the shared composer submits that draft", async () => {
    const inputHandle = createRef<AIChatInputHandle>();
    const onSubmit = vi.fn();
    render(<ChatComposer inputHandleRef={inputHandle} onSubmit={onSubmit} />);

    inputHandle.current?.loadDraft("restored");
    const send = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect(send).toBeEnabled());
    fireEvent.click(send);

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith({ text: "restored" }),
    );
  });
});
