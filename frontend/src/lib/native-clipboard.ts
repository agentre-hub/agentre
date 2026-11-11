import {
  setClipboardImageWriter,
  setClipboardWriter,
} from "@agentre-hub/agentre-ui";

import i18n from "@/i18n";

import { CopyImageToClipboard } from "../../wailsjs/go/app/App";
import { ClipboardSetText } from "../../wailsjs/runtime/runtime";

// 桌面端的复制写系统剪贴板（Wails ClipboardSetText），不走 WebView 的 Clipboard
// API：WKWebView 只在它认定「正在处理用户手势」时放行 writeText，否则拒成
// NotAllowedError——"The request is not allowed by the user agent or the platform
// in the current context…"。原生通道不看手势。Go 侧的错误由 Wails 原样 reject。
async function writeNativeClipboard(text: string): Promise<void> {
  if (!(await ClipboardSetText(text))) {
    throw new Error(i18n.t("clipboard.writeRefused"));
  }
}

async function blobToBase64(blob: Blob): Promise<string> {
  const bytes = new Uint8Array(await blob.arrayBuffer());
  // 分段拼：一次把整张图展开进 fromCharCode 的参数表会撑爆调用栈。
  let binary = "";
  const CHUNK = 0x8000;
  for (let i = 0; i < bytes.length; i += CHUNK) {
    binary += String.fromCharCode(...bytes.subarray(i, i + CHUNK));
  }
  return btoa(binary);
}

// 图片同理：Wails 运行时只有 ClipboardSetText，写图片走自己的 Go 绑定
// （internal/app/clipboard.go → 各平台原生剪贴板）。共享包交来的已经是 PNG。
async function writeNativeClipboardImage(png: Blob): Promise<void> {
  await CopyImageToClipboard(await blobToBase64(png));
}

/** 启动时调一次：之后共享包里的每个复制入口（文字与图片）都写系统剪贴板。 */
export function installNativeClipboard(): void {
  setClipboardWriter(writeNativeClipboard);
  setClipboardImageWriter(writeNativeClipboardImage);
}
