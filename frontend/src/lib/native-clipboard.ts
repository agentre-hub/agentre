import { setClipboardWriter } from "@agentre-hub/agentre-ui";

import i18n from "@/i18n";

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

/** 启动时调一次：之后共享包里的每个复制入口都写系统剪贴板。 */
export function installNativeClipboard(): void {
  setClipboardWriter(writeNativeClipboard);
}
