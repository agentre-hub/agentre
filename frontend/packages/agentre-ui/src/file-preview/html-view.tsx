import { cn } from "../lib/utils";

/**
 * HTML 的渲染档：文件正文原样作为 `srcdoc` 塞进沙箱 iframe（spec「HTML 预览」）。
 *
 * `sandbox` 只给 `allow-scripts`：报告页常靠脚本出图表，所以脚本能跑；但不给
 * `allow-same-origin`，页面是不透明源，拿不到宿主的 cookie / 存储 / Wails 桥接；
 * 不给 `allow-popups` / `allow-top-navigation`，页面里的链接带不走整个应用。
 *
 * 只渲染文件自身文本：相对路径引用的 CSS / JS / 图片解析不到，按浏览器默认行为
 * 缺失（spec 决策 2），面板不另加提示。底色用 `preview-canvas`（两个主题都是白）：
 * 页面按浏览器默认白底写的，跟随暗色主题会把没写背景色的页面画成黑底黑字。
 */
export function HtmlPreview({
  content,
  title,
  className,
}: {
  content: string;
  title: string;
  className?: string;
}) {
  return (
    <iframe
      title={title}
      srcDoc={content}
      sandbox="allow-scripts"
      referrerPolicy="no-referrer"
      className={cn(
        "block h-full w-full border-0 bg-preview-canvas",
        className,
      )}
    />
  );
}
