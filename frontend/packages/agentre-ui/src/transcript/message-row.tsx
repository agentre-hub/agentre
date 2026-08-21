import * as React from "react";
import { Copy } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { copyTextWithToast } from "../lib/clipboard-toast";
import { cn } from "../lib/utils";
import { Button } from "../ui/button";

// MESSAGE_AVATAR_CLASS：头像列的**尺寸契约**(以单聊为准)。MessageRow 自己不画头像,
// 但整行的排版(以及后续分片行的幽灵 gutter)按这个尺寸对齐,所以调用方渲染头像时
// 套上它 —— 杜绝两处各写 size-6 / size-7 的漂移。
export const MESSAGE_AVATAR_CLASS = "size-7 rounded-lg text-2xs";

type MessageRowProps = Omit<React.ComponentProps<"article">, "children"> & {
  /**
   * 头像节点，**必填**。
   *
   * 头像画什么是**身份**问题而不是布局问题：桌面端有 16 色 agent 调色板 +
   * 图标注册表 + 自定义头像图片，agentre-server 侧另有一套。把任何一种塞进这里
   * 都会顺带把宿主的 icon-registry / i18n 拖进本包，还会把那一种身份模型强加给
   * 另一个消费方。MessageRow 只负责把调用方给的节点排进头像列
   * （建议套 MESSAGE_AVATAR_CLASS 保尺寸一致）。
   */
  avatar: React.ReactNode;
  /** 名字行；传 null 时不显名(单聊 user 行)。 */
  name?: React.ReactNode;
  /** 名字行右侧附加内容：时间 / 附加灰字说明。 */
  headerExtra?: React.ReactNode;
  /** 动作行 / token 行的挂载点(复制按钮)。 */
  footer?: React.ReactNode;
  children: React.ReactNode;
};

// MessageRow：单条消息的布局骨架(头像列 + 内容列)。纯展示，不取数据、不决定业务。
// 单条聊天消息在不同 transcript 视图间共用，保证头像尺寸/布局一致，并提供 footer 槽。
function MessageRow({
  avatar,
  name,
  headerExtra,
  footer,
  children,
  className,
  ...props
}: MessageRowProps) {
  const showHeader = name != null || headerExtra != null;
  return (
    <article className={cn("flex gap-3 text-sm", className)} {...props}>
      {avatar}
      <div className="flex min-w-0 max-w-measure flex-1 flex-col gap-1">
        {showHeader ? (
          <div className="flex items-center gap-2">
            {name != null ? (
              <span className="font-semibold">{name}</span>
            ) : null}
            {headerExtra}
          </div>
        ) : null}
        <div data-selectable-text="true" className="flex flex-col gap-2">
          {children}
        </div>
        {footer ? (
          <div className="mt-1 flex flex-wrap items-center gap-2 text-meta text-muted-foreground">
            {footer}
          </div>
        ) : null}
      </div>
    </article>
  );
}

type MessageCopyButtonProps = {
  text: string;
  /** 可见文案，默认 common.copy。 */
  label?: string;
  /** aria-label，默认同可见文案。 */
  ariaLabel?: string;
  /** 复制成功 toast 标题，默认 common.copied。 */
  successTitle?: string;
  /** 复制失败 toast 标题，默认走 copyTextWithToast 内置的 common.copyFailed。 */
  errorTitle?: string;
};

// MessageCopyButton：通用「复制消息正文」按钮。text 为空时不渲染。
function MessageCopyButton({
  text,
  label,
  ariaLabel,
  successTitle,
  errorTitle,
}: MessageCopyButtonProps) {
  const { t } = useUiTranslation();
  if (text.length === 0) return null;
  const visible = label ?? t("common.copy");
  async function handleCopy() {
    await copyTextWithToast(text, {
      successTitle: successTitle ?? t("common.copied"),
      errorTitle,
    });
  }
  return (
    <Button
      type="button"
      variant="ghost"
      size="xs"
      className="h-6 gap-1 px-1.5 text-meta text-muted-foreground"
      aria-label={ariaLabel ?? visible}
      onClick={() => void handleCopy()}
    >
      <Copy data-icon="inline-start" aria-hidden="true" />
      {visible}
    </Button>
  );
}

export { MessageRow, MessageCopyButton };
