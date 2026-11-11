import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}));

vi.mock("sonner", () => sonnerMocks);

import { removeClipboard, restoreClipboardEnv } from "./__testing__/clipboard";
import {
  copyImageWithToast,
  imageToPngBlob,
  setClipboardImageWriter,
} from "./clipboard-image";
import {
  COPY_TOAST_DURATION_MS,
  COPY_TOAST_ERROR_DURATION_MS,
} from "./clipboard-toast";

// 1×1 透明 PNG。前 8 字节是 PNG 签名。
const PNG_BASE64 =
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==";
const PNG_DATA_URL = `data:image/png;base64,${PNG_BASE64}`;
const JPEG_DATA_URL = "data:image/jpeg;base64,/9j/4AAQSkZJRgABAQ==";

async function bytesOf(blob: Blob): Promise<number[]> {
  return Array.from(new Uint8Array(await blob.arrayBuffer()));
}

function base64Bytes(b64: string): number[] {
  return Array.from(atob(b64), (c) => c.charCodeAt(0));
}

const originalClipboardItem = (globalThis as { ClipboardItem?: unknown })
  .ClipboardItem;

class FakeClipboardItem {
  constructor(readonly items: Record<string, Blob | Promise<Blob>>) {}
}

function installImageClipboard(write: ReturnType<typeof vi.fn>) {
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { write, writeText: vi.fn() },
  });
  Object.defineProperty(globalThis, "ClipboardItem", {
    configurable: true,
    writable: true,
    value: FakeClipboardItem,
  });
}

describe("imageToPngBlob", () => {
  it("Given a PNG data URL, When converted, Then the bytes are passed through unchanged as image/png", async () => {
    const rasterize = vi.fn();
    const blob = await imageToPngBlob(
      { dataUrl: PNG_DATA_URL, mediaType: "image/png" },
      { rasterize },
    );

    expect(blob.type).toBe("image/png");
    expect(await bytesOf(blob)).toEqual(base64Bytes(PNG_BASE64));
    expect(rasterize).not.toHaveBeenCalled();
  });

  it("Given a JPEG data URL, When converted, Then it is re-encoded to PNG through the rasterizer", async () => {
    const png = new Blob([new Uint8Array([137, 80, 78, 71])], {
      type: "image/png",
    });
    const rasterize = vi.fn().mockResolvedValue(png);

    const blob = await imageToPngBlob(
      { dataUrl: JPEG_DATA_URL, mediaType: "image/jpeg" },
      { rasterize },
    );

    expect(rasterize).toHaveBeenCalledWith(JPEG_DATA_URL);
    expect(blob).toBe(png);
  });

  it("Given the rasterizer cannot decode the image, When converted, Then the failure propagates", async () => {
    const rasterize = vi.fn().mockRejectedValue(new Error("decode failed"));

    await expect(
      imageToPngBlob({ dataUrl: JPEG_DATA_URL }, { rasterize }),
    ).rejects.toThrow("decode failed");
  });
});

describe("copyImageWithToast", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    setClipboardImageWriter(null);
    restoreClipboardEnv();
    Object.defineProperty(globalThis, "ClipboardItem", {
      configurable: true,
      writable: true,
      value: originalClipboardItem,
    });
  });

  it("Given a host image writer, When an image is copied, Then only the host writer receives the PNG and a success toast shows", async () => {
    const host = vi.fn().mockResolvedValue(undefined);
    const write = vi.fn();
    installImageClipboard(write);
    setClipboardImageWriter(host);

    const copied = await copyImageWithToast({
      dataUrl: PNG_DATA_URL,
      mediaType: "image/png",
    });

    expect(copied).toBe(true);
    expect(host).toHaveBeenCalledTimes(1);
    const png = host.mock.calls[0][0] as Blob;
    expect(png.type).toBe("image/png");
    expect(await bytesOf(png)).toEqual(base64Bytes(PNG_BASE64));
    expect(write).not.toHaveBeenCalled();
    expect(sonnerMocks.toast.success).toHaveBeenCalledWith(
      "Image copied",
      expect.objectContaining({
        duration: COPY_TOAST_DURATION_MS,
        position: "bottom-right",
      }),
    );
    expect(sonnerMocks.toast.error).not.toHaveBeenCalled();
  });

  it("Given the host writer rejects, When an image is copied, Then an error toast shows a classified reason without the raw message", async () => {
    setClipboardImageWriter(
      vi.fn().mockRejectedValue(new Error("NSPasteboard writeObjects failed")),
    );

    const copied = await copyImageWithToast({ dataUrl: PNG_DATA_URL });

    expect(copied).toBe(false);
    expect(sonnerMocks.toast.error).toHaveBeenCalledWith(
      "Failed to copy image",
      expect.objectContaining({
        description: "Copy did not go through this time. Please try again.",
        duration: COPY_TOAST_ERROR_DURATION_MS,
        position: "bottom-right",
      }),
    );
    expect(JSON.stringify(sonnerMocks.toast.error.mock.calls)).not.toContain(
      "NSPasteboard",
    );
    expect(sonnerMocks.toast.success).not.toHaveBeenCalled();
  });

  it("Given the browser rejects with its own message, When an image is copied, Then the raw error goes to the log, not the toast", async () => {
    const raw = new DOMException("Document is not focused.", "NotAllowedError");
    installImageClipboard(vi.fn().mockRejectedValue(raw));
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});

    try {
      await copyImageWithToast({ dataUrl: PNG_DATA_URL });

      expect(warn).toHaveBeenCalledWith(expect.any(String), raw);
      expect(JSON.stringify(sonnerMocks.toast.error.mock.calls)).not.toContain(
        "Document is not focused",
      );
    } finally {
      warn.mockRestore();
    }
  });

  it("Given no host writer but a browser clipboard, When an image is copied, Then navigator.clipboard.write receives an image/png ClipboardItem", async () => {
    const write = vi.fn().mockResolvedValue(undefined);
    installImageClipboard(write);

    const copied = await copyImageWithToast({ dataUrl: PNG_DATA_URL });

    expect(copied).toBe(true);
    expect(write).toHaveBeenCalledTimes(1);
    const [items] = write.mock.calls[0] as [FakeClipboardItem[]];
    expect(items).toHaveLength(1);
    expect(Object.keys(items[0].items)).toEqual(["image/png"]);
    const png = await items[0].items["image/png"];
    expect(png.type).toBe("image/png");
    expect(await bytesOf(png)).toEqual(base64Bytes(PNG_BASE64));
  });

  it("Given the browser refuses the write, When an image is copied, Then the permission-denied reason shows", async () => {
    installImageClipboard(
      vi
        .fn()
        .mockRejectedValue(new DOMException("not allowed", "NotAllowedError")),
    );

    const copied = await copyImageWithToast({ dataUrl: PNG_DATA_URL });

    expect(copied).toBe(false);
    expect(sonnerMocks.toast.error).toHaveBeenCalledWith(
      "Failed to copy image",
      expect.objectContaining({
        description:
          "The browser did not allow clipboard writes. Click the page and try again, or select and copy manually.",
      }),
    );
  });

  it("Given an insecure context without any clipboard API, When an image is copied, Then it fails with the HTTPS hint and never falls back to execCommand", async () => {
    removeClipboard();
    const execCommand = vi.fn().mockReturnValue(true);
    Object.defineProperty(document, "execCommand", {
      configurable: true,
      value: execCommand,
    });

    const copied = await copyImageWithToast({ dataUrl: PNG_DATA_URL });

    expect(copied).toBe(false);
    expect(execCommand).not.toHaveBeenCalled();
    expect(sonnerMocks.toast.error).toHaveBeenCalledWith(
      "Failed to copy image",
      expect.objectContaining({
        description:
          "This page cannot copy images. Open it over HTTPS to copy images.",
      }),
    );
  });
});

describe("复制图片的对外契约", () => {
  it("宿主装口与复制入口从 barrel 出得去", async () => {
    const pkg = await import("../index");
    const missing = [
      "copyImageToClipboard",
      "copyImageWithToast",
      "imageToPngBlob",
      "setClipboardImageWriter",
    ].filter((name) => !(name in pkg));

    expect(missing).toEqual([]);
  });
});
