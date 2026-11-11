import i18n from "i18next";
import { toast } from "sonner";

import { AGENTRE_UI_NAMESPACE } from "../i18n";
import {
  COPY_TOAST_DURATION_MS,
  COPY_TOAST_ERROR_DURATION_MS,
  NoClipboardChannelError,
  copyFailureDescription,
} from "./clipboard-toast";

/** 要复制的那张图：转录图片块和输入框附件都是 data URL。 */
export type CopyableImage = {
  dataUrl: string;
  mediaType?: string;
};

/**
 * 宿主自己的图片剪贴板通道：收到的一律是 PNG，resolve 表示写进去了，reject
 * 带上没写进去的原因。
 *
 * 和文字的 `setClipboardWriter` 同一个结构、同一个理由——桌面端的 WKWebView
 * 不看手势就拒 Clipboard API，桌面端手里有原生通道。没装就走浏览器的
 * `navigator.clipboard.write`。
 */
export type ClipboardImageWriter = (png: Blob) => Promise<void>;

let hostClipboardImageWriter: ClipboardImageWriter | null = null;

/** 装上（或传 `null` 卸下）宿主的图片剪贴板通道。 */
export function setClipboardImageWriter(
  writer: ClipboardImageWriter | null,
): void {
  hostClipboardImageWriter = writer;
}

type PngOptions = {
  /** 非 PNG 的图怎么重编码成 PNG。默认画到 canvas 上再导出。 */
  rasterize?: (dataUrl: string) => Promise<Blob>;
};

const PNG_DATA_URL_PREFIX = "data:image/png;base64,";

function decodeBase64(b64: string): Uint8Array<ArrayBuffer> {
  const binary = atob(b64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

/**
 * 画到 canvas 上导出 PNG。动图只剩第一帧（spec 决策 6 接受的代价）；
 * 解不出来的图在 `onerror` 处 reject。
 */
function rasterizeWithCanvas(dataUrl: string): Promise<Blob> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => {
      const canvas = document.createElement("canvas");
      canvas.width = img.naturalWidth;
      canvas.height = img.naturalHeight;
      const ctx = canvas.getContext("2d");
      if (!ctx) {
        reject(new Error("canvas 2d context unavailable"));
        return;
      }
      ctx.drawImage(img, 0, 0);
      canvas.toBlob((blob) => {
        if (blob) resolve(blob);
        else reject(new Error("canvas toBlob returned null"));
      }, "image/png");
    };
    img.onerror = () => reject(new Error("image failed to decode"));
    img.src = dataUrl;
  });
}

/**
 * 把图转成剪贴板通用的 PNG（spec 决策 6）。已经是 base64 PNG 的直接解码，
 * 字节原样；其余（JPEG / WebP / 非 base64）重编码。
 */
export async function imageToPngBlob(
  image: CopyableImage,
  { rasterize = rasterizeWithCanvas }: PngOptions = {},
): Promise<Blob> {
  if (image.dataUrl.startsWith(PNG_DATA_URL_PREFIX)) {
    return new Blob(
      [decodeBase64(image.dataUrl.slice(PNG_DATA_URL_PREFIX.length))],
      { type: "image/png" },
    );
  }
  return rasterize(image.dataUrl);
}

function browserCanWriteImages(): boolean {
  return (
    typeof navigator.clipboard?.write === "function" &&
    typeof ClipboardItem !== "undefined"
  );
}

/**
 * 复制本身，不带反馈；写不进去时抛出，原因交给 `copyFailureDescription` 分类。
 *
 * 浏览器那条路把 **Promise<Blob>** 交给 ClipboardItem、同步调 `write`：
 * Safari 只在用户手势的同一拍里放行剪贴板写入，先 await 转码再 write 会被拒。
 */
export async function copyImageToClipboard(
  image: CopyableImage,
): Promise<void> {
  if (hostClipboardImageWriter) {
    await hostClipboardImageWriter(await imageToPngBlob(image));
    return;
  }
  if (!browserCanWriteImages()) {
    // 非安全上下文整个 navigator.clipboard 都不存在。文字有 execCommand 兜底，
    // 图片没有——直接失败。
    throw new NoClipboardChannelError();
  }
  await navigator.clipboard.write([
    new ClipboardItem({ "image/png": imageToPngBlob(image) }),
  ]);
}

export async function copyImageWithToast(
  image: CopyableImage,
): Promise<boolean> {
  try {
    await copyImageToClipboard(image);
    toast.success(i18n.t("imageCopy.copied", { ns: AGENTRE_UI_NAMESPACE }), {
      duration: COPY_TOAST_DURATION_MS,
      position: "bottom-right",
    });
    return true;
  } catch (err) {
    toast.error(i18n.t("imageCopy.failed", { ns: AGENTRE_UI_NAMESPACE }), {
      description: copyFailureDescription(err, "imageCopy.insecureContext"),
      duration: COPY_TOAST_ERROR_DURATION_MS,
      position: "bottom-right",
    });
    return false;
  }
}
