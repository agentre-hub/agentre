import * as React from "react";
import { ExternalLink, Pencil, Trash2 } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { statusConfig } from "../transcript/agent-status";
import type { AgentStatus } from "../transcript/agent-status";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "../ui/context-menu";
import { StatusDot } from "../ui/status-dot";

/**
 * 宿主的路由链接渲染器收到的一组 props。包负责**长什么样**（className / 内容 /
 * aria），宿主只负责**用什么元素跳**（react-router 的 `<Link to>`、Next 的
 * `<Link href>`、或者什么都不给用原生 `<a>`）。
 */
type SessionRowLinkProps = {
  href: string;
  className: string;
  children: React.ReactNode;
  "aria-current"?: "true";
  "aria-hidden"?: React.AriaAttributes["aria-hidden"];
  "aria-disabled"?: "true";
  tabIndex?: number;
  onClick?: React.MouseEventHandler<HTMLElement>;
};

type SessionRowLinkRenderer = (props: SessionRowLinkProps) => React.ReactNode;

type SessionRowProps = Omit<React.ComponentProps<"button">, "onClick"> & {
  selected?: boolean;
  status: AgentStatus;
  title: string;
  trailingLabel: string;
  onClick?: React.MouseEventHandler<HTMLElement>;
  /**
   * 行首插槽，紧跟在状态点之后。索引按一维分组时用它放**另一维**：按项目分组时是
   * agent 头像，按 Agent 分组时是项目色文件夹字形（宿主决定放什么，包只留位置）。
   *
   * 不传就**不占位** —— 给一个空槽会让不需要它的那些档凭空多一段左边距。
   */
  leading?: React.ReactNode;
  /**
   * 第二行小字。给了行就变成两行：第一行状态 + 标题 + 尾标，第二行是它。
   *
   * 两端都要：桌面索引的「按时间」档没有组头，两维只能落在行里；agentre-server 的
   * 会话行第二行是「设备 · 后端」。内容由宿主拼，包不认识它的语义。
   */
  secondaryLabel?: string;
  /**
   * 行的目标地址。给了就渲染成链接而不是按钮。
   *
   * 桌面端不给：点一行是「开一个标签页」，没有地址可言。浏览器宿主必须给 ——
   * 否则中键 / ⌘ 点击 / 右键「复制链接地址」会**静默**失效（点得动，只是少了
   * 三件事，不报任何错）。
   */
  href?: string;
  /** 见 `SessionRowLinkRenderer`。只在 `href` 存在时起作用；不给就用原生 `<a>`。 */
  renderLink?: SessionRowLinkRenderer;
  // 会话行右键菜单（可选）：任一 handler 存在即在行上渲染 ContextMenu；
  // 不传时保持纯按钮 —— 项目页（SessionGroup 不传这些 props）沿用旧行为。
  onOpenInNewTab?: () => void;
  onRenameSession?: () => void;
  onDeleteSession?: () => void;
};

function SessionRow({
  "aria-hidden": ariaHidden,
  className,
  disabled,
  selected = false,
  status,
  title,
  trailingLabel,
  href,
  renderLink,
  leading,
  secondaryLabel,
  onClick,
  onOpenInNewTab,
  onRenameSession,
  onDeleteSession,
  ...props
}: SessionRowProps) {
  const { t } = useUiTranslation();
  const config = statusConfig[status];
  const hiddenFromAccessibility = ariaHidden === true || ariaHidden === "true";
  const hasContextMenu = Boolean(
    onOpenInNewTab || onRenameSession || onDeleteSession,
  );

  const rowClassName = cn(
    "flex w-full cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs outline-none transition-colors hover:bg-sidebar-active-bg focus-visible:ring-[3px] focus-visible:ring-ring/50",
    selected && "bg-primary-soft text-primary-text",
    className,
  );

  const titleColumn = (
    <span
      className={cn(
        "min-w-0 flex-1",
        selected ? "font-medium text-primary-text" : "text-foreground",
      )}
    >
      <span className="block truncate">{title}</span>
      {secondaryLabel ? (
        <span className="mt-0.5 block truncate text-2xs text-muted-foreground">
          {secondaryLabel}
        </span>
      ) : null}
    </span>
  );

  const content = (
    <>
      <StatusDot
        status={status}
        size="xs"
        {...(hiddenFromAccessibility
          ? { "aria-hidden": true, "aria-label": undefined }
          : {})}
      />
      {leading}
      {titleColumn}
      <span
        className={cn(
          "shrink-0 font-mono text-2xs",
          selected ? "text-primary-text" : config.textClassName,
        )}
      >
        {trailingLabel}
      </span>
    </>
  );

  let row: React.ReactNode;
  if (href === undefined) {
    row = (
      <button
        type="button"
        aria-hidden={ariaHidden}
        disabled={disabled}
        aria-current={selected ? "true" : undefined}
        className={rowClassName}
        onClick={onClick}
        {...props}
      >
        {content}
      </button>
    );
  } else {
    // 链接没有 `disabled` 可倚靠：折叠分组里的行必须靠 aria-disabled + tabIndex=-1
    // 退出可达性与 Tab 序，否则一条看不见的行照样能被键盘走到、被回车激活。
    const linkProps: SessionRowLinkProps = {
      href,
      className: rowClassName,
      children: content,
      "aria-current": selected ? "true" : undefined,
      "aria-hidden": ariaHidden,
      ...(disabled
        ? {
            "aria-disabled": "true" as const,
            tabIndex: -1,
            onClick: (e: React.MouseEvent<HTMLElement>) => e.preventDefault(),
          }
        : { onClick }),
    };
    row = renderLink ? renderLink(linkProps) : <a {...linkProps} />;
  }

  if (!hasContextMenu) return row;

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>{row}</ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuItem onSelect={onRenameSession}>
          <Pencil className="size-4" aria-hidden="true" />
          <span>{t("sessionRow.menu.rename")}</span>
        </ContextMenuItem>
        <ContextMenuItem onSelect={onOpenInNewTab}>
          <ExternalLink className="size-4" aria-hidden="true" />
          <span>{t("sessionRow.menu.openInNewTab")}</span>
        </ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem variant="destructive" onSelect={onDeleteSession}>
          <Trash2 className="size-4" aria-hidden="true" />
          <span>{t("sessionRow.menu.delete")}</span>
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

export { SessionRow };
export type { SessionRowLinkProps, SessionRowLinkRenderer, SessionRowProps };
