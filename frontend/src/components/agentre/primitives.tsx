import * as React from "react";
import { Icon as IconifyIconCmp } from "@iconify/react";
import type { IconifyIcon } from "@iconify/types";
import { type LucideIcon } from "lucide-react";
// StatusDot 随会话索引搬进了共享包（agentre-server 的会话列表要用同一个圆点）；
// 这里转发，仓库内 7 个引用点不必改指包。
import {
  AgentAvatar as UiAgentAvatar,
  StatusDot,
} from "@agentre-ai/agentre-ui";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { DeviceTag } from "./device-tag";
import { hasIcon, iconForKey } from "./icon-registry";
import { type AgentColor, type AgentStatus, statusConfig } from "./types";

/**
 * 身份方块的实现已经搬进共享包 `@agentre-ai/agentre-ui`（规格 2026-08-21
 * 「身份字形归一」）—— 同一枚记号此前在桌面端、包里和 agentre-server 各有一份。
 *
 * 这里只剩**宿主那一半**：把 `avatarIcon` 这个 icon key 经 icon-registry 解成一个
 * 画好的节点递进去。图标表是宿主的产品决定（383 行的 lucide 清单），不进包。
 * 仓库内 26 个引用点因此一个字都不用改。
 */
type AgentAvatarSize = "sm" | "md" | "lg";

type AgentAvatarProps = Omit<React.ComponentProps<"span">, "color"> & {
  name: string;
  initials?: string;
  color?: AgentColor;
  size?: AgentAvatarSize;
  avatarDataUrl?: string;
  avatarIcon?: string;
};

/** icon key → 画好的图标节点；key 空或不在注册表里就没有节点（退回首字母）。 */
export function agentIconNode(
  avatarIcon: string | null | undefined,
): React.ReactNode {
  if (!avatarIcon || !hasIcon(avatarIcon)) return undefined;
  return React.createElement(iconForKey(avatarIcon), {
    className: "size-[60%]",
    "aria-hidden": true,
  });
}

function AgentAvatar({ avatarIcon, ...props }: AgentAvatarProps) {
  return <UiAgentAvatar {...props} icon={agentIconNode(avatarIcon)} />;
}

type StatusPillProps = React.ComponentProps<"span"> & {
  status: AgentStatus;
  label?: string;
};

function StatusPill({ className, label, status, ...props }: StatusPillProps) {
  const config = statusConfig[status];

  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-sm px-1.5 py-0.5 font-mono text-2xs font-semibold",
        config.pillClassName,
        className,
      )}
      {...props}
    >
      <StatusDot status={status} size="xs" />
      {label ?? config.label}
    </span>
  );
}

type SidebarIcon = LucideIcon | IconifyIcon;

function isIconifyIcon(icon: SidebarIcon): icon is IconifyIcon {
  return typeof icon === "object" && icon !== null && "body" in icon;
}

function renderSidebarIcon(icon: SidebarIcon) {
  if (isIconifyIcon(icon)) {
    return <IconifyIconCmp icon={icon} data-icon="only" aria-hidden="true" />;
  }
  const IconComponent = icon;
  return <IconComponent data-icon="only" aria-hidden="true" />;
}

type SidebarButtonProps = Omit<
  React.ComponentProps<typeof Button>,
  "children"
> & {
  active?: boolean;
  /** 图标右上角的小圆点：表示这一项底下有新东西，不解释是什么。 */
  badge?: boolean;
  icon: SidebarIcon;
  label: string;
};

function SidebarButton({
  active = false,
  badge = false,
  className,
  icon,
  label,
  ...props
}: SidebarButtonProps) {
  const tooltipId = React.useId();
  const describedBy = [props["aria-describedby"], tooltipId]
    .filter(Boolean)
    .join(" ");

  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      aria-current={active ? "page" : undefined}
      aria-describedby={describedBy}
      aria-label={label}
      className={cn(
        "group relative size-10 overflow-visible rounded-lg text-sidebar-icon hover:bg-rail-accent hover:text-sidebar-accent-foreground [&_svg:not([class*='size-'])]:size-[18px]",
        active &&
          "bg-primary-soft text-sidebar-icon-active shadow-xs hover:bg-primary-soft hover:text-sidebar-icon-active",
        className,
      )}
      {...props}
    >
      {renderSidebarIcon(icon)}
      {badge ? (
        <span
          data-slot="sidebar-badge"
          aria-hidden="true"
          className="absolute right-1.5 top-1.5 size-[7px] rounded-full bg-primary ring-2 ring-rail"
        />
      ) : null}
      <span
        id={tooltipId}
        role="tooltip"
        className="pointer-events-none absolute left-full top-1/2 z-50 ml-2 -translate-y-1/2 translate-x-1 scale-95 whitespace-nowrap rounded-md border border-border bg-popover px-2 py-1 text-xs font-medium text-popover-foreground opacity-0 shadow-md transition-[opacity,transform] duration-150 [transition-delay:0ms] group-focus-visible:translate-x-0 group-focus-visible:scale-100 group-focus-visible:opacity-100 group-focus-visible:[transition-delay:0ms] group-hover:translate-x-0 group-hover:scale-100 group-hover:opacity-100 group-hover:[transition-delay:300ms]"
      >
        <span
          aria-hidden="true"
          data-slot="tooltip-arrow"
          className="absolute -left-1 top-1/2 size-2 -translate-y-1/2 rotate-45 border-b border-l border-border bg-popover"
        />
        <span className="relative">{label}</span>
      </span>
    </Button>
  );
}

export { AgentAvatar, DeviceTag, SidebarButton, StatusDot, StatusPill };
