import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const copyMocks = vi.hoisted(() => ({
  copyImageWithToast: vi.fn(),
}));

vi.mock("../lib/clipboard-image", () => copyMocks);

import { TooltipProvider } from "../ui/tooltip";
import {
  TranscriptRenderContext,
  TranscriptRowView,
} from "./transcript-row-view";
import type { TranscriptBlock } from "./dto";
import type { TranscriptRow } from "./transcript-rows";

function image(dataUrl: string, name?: string): TranscriptBlock {
  return {
    type: "image",
    image: { dataUrl, mediaType: "image/png", name },
  } as TranscriptBlock;
}

const A = image("data:image/png;base64,AAAA", "登录页.png");
const B = image("data:image/png;base64,BBBB", "设置页.png");

function imageRow(block: TranscriptBlock, group: TranscriptBlock[]) {
  return {
    key: "message:3:image:0",
    messageId: 3,
    message: {
      id: 3,
      role: "user",
      blocks: group,
      createtime: 0,
      durationMs: 0,
      model: "",
      promptTokens: 0,
      completionTokens: 0,
      cachedTokens: 0,
      cacheCreationTokens: 0,
      reasoningTokens: 0,
    },
    item: { type: "image", uiStateKey: "message:3:image:0", block, group },
    isFirstOfMessage: true,
    isLastOfMessage: true,
    autonomous: false,
  } as unknown as TranscriptRow;
}

function renderImageRow(block: TranscriptBlock, group: TranscriptBlock[]) {
  render(
    <TooltipProvider>
      <TranscriptRenderContext.Provider
        value={{ agentName: "Agentre", agentAvatar: <span />, sessionId: 42 }}
      >
        <TranscriptRowView
          row={imageRow(block, group)}
          liveTail=""
          liveBlocks={undefined}
          liveRetry={null}
          showIndicator={false}
          compacting={false}
          reconnecting={false}
        />
      </TranscriptRenderContext.Provider>
    </TooltipProvider>,
  );
}

describe("转录图片块", () => {
  beforeEach(() => {
    copyMocks.copyImageWithToast.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("Given an image block, When rendered, Then the thumbnail is a button named after the file and no longer a new-tab link", () => {
    renderImageRow(B, [A, B]);

    expect(
      screen.getByRole("button", { name: "View image: 设置页.png" }),
    ).toBeInTheDocument();
    expect(document.querySelector('a[target="_blank"]')).toBeNull();
  });

  it("Given an image without a name, When rendered, Then the thumbnail button is just 'View image'", () => {
    const unnamed = image("data:image/png;base64,CCCC");
    renderImageRow(unnamed, [unnamed]);

    expect(
      screen.getByRole("button", { name: "View image" }),
    ).toBeInTheDocument();
  });

  it("Given two images in the message, When the second thumbnail is clicked, Then the viewer opens on it with the message's group", () => {
    renderImageRow(B, [A, B]);

    fireEvent.click(
      screen.getByRole("button", { name: "View image: 设置页.png" }),
    );

    expect(screen.getByRole("dialog")).toHaveAccessibleName(
      "Image viewer: 设置页.png",
    );
    expect(screen.getByText("2 / 2")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Previous image" }));
    expect(screen.getByText("1 / 2")).toBeInTheDocument();
  });

  it("Given the copy button, When clicked and the copy succeeds, Then it copies this image and shows a check that reverts after about 1.5s", async () => {
    vi.useFakeTimers();
    copyMocks.copyImageWithToast.mockResolvedValue(true);
    renderImageRow(A, [A, B]);

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Copy image" }));
    });

    expect(copyMocks.copyImageWithToast).toHaveBeenCalledWith({
      dataUrl: "data:image/png;base64,AAAA",
      mediaType: "image/png",
      name: "登录页.png",
    });
    expect(screen.queryByRole("dialog")).toBeNull();
    const button = screen.getByRole("button", { name: "Copy image" });
    expect(button).toHaveAttribute("data-copied", "true");

    act(() => {
      vi.advanceTimersByTime(1500);
    });
    expect(button).not.toHaveAttribute("data-copied");
  });

  it("Given the copy fails, When clicked, Then no check is shown", async () => {
    copyMocks.copyImageWithToast.mockResolvedValue(false);
    renderImageRow(A, [A]);

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Copy image" }));
    });

    expect(
      screen.getByRole("button", { name: "Copy image" }),
    ).not.toHaveAttribute("data-copied");
  });
});
