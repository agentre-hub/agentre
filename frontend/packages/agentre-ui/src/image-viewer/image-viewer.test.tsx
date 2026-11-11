import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import * as React from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const copyMocks = vi.hoisted(() => ({
  copyImageWithToast: vi.fn().mockResolvedValue(true),
}));

vi.mock("../lib/clipboard-image", () => copyMocks);

import { ImageViewer, type ViewerImage } from "./image-viewer";

const IMAGES: ViewerImage[] = [
  {
    dataUrl: "data:image/png;base64,AAAA",
    mediaType: "image/png",
    name: "登录页.png",
  },
  {
    dataUrl: "data:image/jpeg;base64,BBBB",
    mediaType: "image/jpeg",
    name: "设置页.jpg",
  },
  { dataUrl: "data:image/webp;base64,CCCC", mediaType: "image/webp" },
];

/**
 * 宿主的用法：一个触发按钮记下要打开第几张，查看器关掉后焦点回到它。
 */
function Harness({
  images = IMAGES,
  startIndex = 0,
}: {
  images?: ViewerImage[];
  startIndex?: number;
}) {
  const [open, setOpen] = React.useState(false);
  const triggerRef = React.useRef<HTMLButtonElement>(null);
  return (
    <>
      <button ref={triggerRef} type="button" onClick={() => setOpen(true)}>
        thumb
      </button>
      <ImageViewer
        images={images}
        startIndex={startIndex}
        open={open}
        onOpenChange={setOpen}
        returnFocusTo={triggerRef}
      />
    </>
  );
}

function openViewer(props?: React.ComponentProps<typeof Harness>) {
  render(<Harness {...props} />);
  fireEvent.click(screen.getByRole("button", { name: "thumb" }));
  return screen.getByRole("dialog");
}

function loadImage(width: number, height: number) {
  const img = screen.getByTestId("image-viewer-image") as HTMLImageElement;
  Object.defineProperty(img, "naturalWidth", {
    configurable: true,
    value: width,
  });
  Object.defineProperty(img, "naturalHeight", {
    configurable: true,
    value: height,
  });
  fireEvent.load(img);
  return img;
}

const originalUserAgent = navigator.userAgent;

function setUserAgent(ua: string) {
  Object.defineProperty(navigator, "userAgent", {
    configurable: true,
    value: ua,
  });
}

describe("ImageViewer", () => {
  beforeEach(() => {
    copyMocks.copyImageWithToast.mockClear();
    setUserAgent(
      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15",
    );
  });

  afterEach(() => {
    setUserAgent(originalUserAgent);
    window.getSelection()?.removeAllRanges();
  });

  it("Given a group of images, When opened at the second one, Then it shows that image with its name, page number and an accessible dialog name", () => {
    const dialog = openViewer({ startIndex: 1 });

    expect(dialog).toHaveAccessibleName("Image viewer: 设置页.jpg");
    expect(screen.getByTestId("image-viewer-image")).toHaveAttribute(
      "src",
      IMAGES[1].dataUrl,
    );
    expect(screen.getByText("设置页.jpg")).toBeInTheDocument();
    expect(screen.getByText("2 / 3")).toBeInTheDocument();
  });

  it("Given the image has loaded, When the top bar renders, Then it shows natural size and format", () => {
    openViewer();
    loadImage(1600, 1000);

    expect(screen.getByText("1600 × 1000 · PNG")).toBeInTheDocument();
  });

  it("Given an image without a name, When opened, Then no file name shows and the dialog name has no suffix", () => {
    const dialog = openViewer({ startIndex: 2 });

    expect(dialog).toHaveAccessibleName("Image viewer");
  });

  it("Given several images, When navigating with buttons and arrow keys, Then it moves without wrapping and hides the button at each end", () => {
    const dialog = openViewer();

    expect(screen.queryByRole("button", { name: "Previous image" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Next image" }));
    expect(screen.getByText("2 / 3")).toBeInTheDocument();

    fireEvent.keyDown(dialog, { key: "ArrowRight" });
    expect(screen.getByText("3 / 3")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Next image" })).toBeNull();

    fireEvent.keyDown(dialog, { key: "ArrowRight" });
    expect(screen.getByText("3 / 3")).toBeInTheDocument();

    fireEvent.keyDown(dialog, { key: "ArrowLeft" });
    fireEvent.click(screen.getByRole("button", { name: "Previous image" }));
    expect(screen.getByText("1 / 3")).toBeInTheDocument();
    fireEvent.keyDown(dialog, { key: "ArrowLeft" });
    expect(screen.getByText("1 / 3")).toBeInTheDocument();
  });

  it("Given a single image, When opened, Then there is no page number, no navigation and no switch hint", () => {
    openViewer({ images: [IMAGES[0]] });

    expect(screen.queryByText("1 / 1")).toBeNull();
    expect(screen.queryByRole("button", { name: "Previous image" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Next image" })).toBeNull();
    expect(screen.getByTestId("image-viewer-hint")).toHaveTextContent(
      "Esc close · ⌘C copy · Click image to toggle actual size",
    );
  });

  it("Given several images on a Mac, When the hint renders, Then it lists close, switch, ⌘C copy and toggle", () => {
    openViewer();

    expect(screen.getByTestId("image-viewer-hint")).toHaveTextContent(
      "Esc close · ← → switch · ⌘C copy · Click image to toggle actual size",
    );
  });

  it("Given Windows or Linux, When the hint renders, Then it says Ctrl+C", () => {
    setUserAgent(
      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
    );
    openViewer();

    expect(screen.getByTestId("image-viewer-hint")).toHaveTextContent(
      "Ctrl+C copy",
    );
  });

  it("Given the viewer is open, When Esc is pressed, Then it closes and focus returns to the trigger", async () => {
    openViewer();

    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });

    expect(screen.queryByRole("dialog")).toBeNull();
    // Radix FocusScope 卸载时在下一个宏任务里交还焦点。
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "thumb" })).toHaveFocus(),
    );
  });

  it("Given the viewer is open, When the close button is clicked, Then it closes and focus returns to the trigger", async () => {
    openViewer();

    fireEvent.click(screen.getByRole("button", { name: "Close" }));

    expect(screen.queryByRole("dialog")).toBeNull();
    // Radix FocusScope 卸载时在下一个宏任务里交还焦点。
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "thumb" })).toHaveFocus(),
    );
  });

  it("Given the viewer is open, When the backdrop outside the image is clicked, Then it closes; clicking the image does not", () => {
    openViewer();

    fireEvent.click(screen.getByTestId("image-viewer-image"));
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("image-viewer-stage"));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("Given fit-to-window by default, When the image or the toggle button is clicked, Then it switches between actual size and fit, and resets on another image", () => {
    openViewer();
    const stage = screen.getByTestId("image-viewer-stage");
    expect(stage).toHaveAttribute("data-display", "fit");

    fireEvent.click(screen.getByTestId("image-viewer-image"));
    expect(stage).toHaveAttribute("data-display", "actual");
    expect(
      screen.getByRole("button", { name: "Fit to window" }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Fit to window" }));
    expect(stage).toHaveAttribute("data-display", "fit");

    fireEvent.click(screen.getByRole("button", { name: "Actual size" }));
    expect(stage).toHaveAttribute("data-display", "actual");

    fireEvent.keyDown(screen.getByRole("dialog"), { key: "ArrowRight" });
    expect(screen.getByTestId("image-viewer-stage")).toHaveAttribute(
      "data-display",
      "fit",
    );
  });

  it("Given no text is selected, When ⌘C is pressed, Then the current image is copied", () => {
    openViewer({ startIndex: 1 });

    fireEvent.keyDown(screen.getByRole("dialog"), { key: "c", metaKey: true });

    expect(copyMocks.copyImageWithToast).toHaveBeenCalledWith(IMAGES[1]);
  });

  it("Given Windows, When Ctrl+C is pressed, Then the current image is copied", () => {
    setUserAgent(
      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
    );
    openViewer();

    fireEvent.keyDown(screen.getByRole("dialog"), { key: "c", ctrlKey: true });

    expect(copyMocks.copyImageWithToast).toHaveBeenCalledWith(IMAGES[0]);
  });

  it("Given text is selected inside the viewer, When ⌘C is pressed, Then the image copy does not intercept it", () => {
    openViewer();
    const name = screen.getByText("登录页.png");
    const range = document.createRange();
    range.selectNodeContents(name);
    window.getSelection()?.addRange(range);

    const event = fireEvent.keyDown(screen.getByRole("dialog"), {
      key: "c",
      metaKey: true,
    });

    expect(event).toBe(true); // 没有 preventDefault，浏览器照常复制文字
    expect(copyMocks.copyImageWithToast).not.toHaveBeenCalled();
  });

  it("Given text left selected outside the viewer, When ⌘C is pressed inside it, Then the current image is copied instead of that text", () => {
    const outside = document.createElement("p");
    outside.textContent = "转录里先前选中的一段";
    document.body.appendChild(outside);
    try {
      const range = document.createRange();
      range.selectNodeContents(outside);
      window.getSelection()?.addRange(range);
      openViewer({ startIndex: 1 });

      const event = fireEvent.keyDown(screen.getByRole("dialog"), {
        key: "c",
        metaKey: true,
      });

      expect(event).toBe(false); // preventDefault：不让浏览器去复制外面那段文字
      expect(copyMocks.copyImageWithToast).toHaveBeenCalledWith(IMAGES[1]);
    } finally {
      outside.remove();
    }
  });

  it("Given the copy button, When clicked, Then the current image is copied", () => {
    openViewer();

    fireEvent.click(screen.getByRole("button", { name: "Copy" }));

    expect(copyMocks.copyImageWithToast).toHaveBeenCalledWith(IMAGES[0]);
  });

  it("Given the image cannot be decoded, When it errors, Then it shows the failure text and hides copy and size, then recovers on another image", () => {
    openViewer();

    fireEvent.error(screen.getByTestId("image-viewer-image"));

    expect(screen.getByText("Image cannot be displayed")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Copy" })).toBeNull();
    expect(screen.queryByText(/×/)).toBeNull();
    expect(screen.getByText("1 / 3")).toBeInTheDocument();

    act(() => {
      fireEvent.keyDown(screen.getByRole("dialog"), { key: "ArrowRight" });
    });
    expect(screen.queryByText("Image cannot be displayed")).toBeNull();
    expect(screen.getByRole("button", { name: "Copy" })).toBeInTheDocument();
  });

  it("Given reduced motion, When the viewer renders, Then every animation utility is under motion-safe", () => {
    openViewer();

    const animated = [
      document.querySelector('[data-slot="image-viewer-overlay"]'),
      document.querySelector('[data-slot="image-viewer-content"]'),
    ];
    for (const el of animated) {
      expect(el).not.toBeNull();
      const bare = [...el!.classList].filter(
        (c) =>
          /(animate-|fade-|zoom-)/.test(c) && !c.startsWith("motion-safe:"),
      );
      expect(bare).toEqual([]);
    }
  });
});

describe("ImageViewer · 开着时组变小", () => {
  it("Given the viewer is on the last image, When the host shrinks the group, Then it shows the last remaining image instead of an empty overlay", () => {
    const { rerender } = render(
      <ImageViewer
        images={IMAGES}
        startIndex={2}
        open
        onOpenChange={vi.fn()}
      />,
    );
    expect(screen.getByText("3 / 3")).toBeInTheDocument();

    rerender(
      <ImageViewer
        images={IMAGES.slice(0, 1)}
        startIndex={2}
        open
        onOpenChange={vi.fn()}
      />,
    );

    expect(screen.getByRole("dialog")).toHaveAccessibleName(
      "Image viewer: 登录页.png",
    );
    expect(screen.getByTestId("image-viewer-image")).toHaveAttribute(
      "src",
      IMAGES[0].dataUrl,
    );
  });
});

describe("查看器的对外契约", () => {
  it("从 barrel 出得去", async () => {
    const pkg = await import("../index");
    expect("ImageViewer" in pkg).toBe(true);
  });
});
