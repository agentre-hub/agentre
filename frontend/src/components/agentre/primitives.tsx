import * as React from "react";
import { Icon as IconifyIconCmp } from "@iconify/react";
import type { IconifyIcon } from "@iconify/types";
import { type LucideIcon } from "lucide-react";
// StatusDot 随会话索引搬进了共享包（agentre-server 的会话列表要用同一个圆点）；
// 这里转发，仓库内 7 个引用点不必改指包。
import { StatusDot } from "@agentre-ai/agentre-ui";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { DeviceTag } from "./device-tag";
import { hasIcon, iconForKey } from "./icon-registry";
import {
  agentColorClassNames,
  type AgentColor,
  type AgentStatus,
  statusConfig,
} from "./types";

type AgentAvatarSize = "sm" | "md" | "lg";

const avatarSizeClassNames: Record<AgentAvatarSize, string> = {
  sm: "size-6 rounded-md text-2xs",
  md: "size-8 rounded-lg text-sm",
  lg: "size-10 rounded-lg text-sm",
};

function getInitials(name: string) {
  const trimmed = name.trim();

  if (!trimmed) {
    return "?";
  }

  const parts = trimmed.split(/\s+/);
  if (parts.length > 1 && /^[a-z0-9]/i.test(parts[0])) {
    return parts
      .slice(0, 2)
      .map((part) => part[0])
      .join("")
      .toUpperCase();
  }

  return trimmed.slice(0, 1).toUpperCase();
}

type AgentAvatarProps = React.ComponentProps<"div"> & {
  name: string;
  initials?: string;
  color?: AgentColor;
  size?: AgentAvatarSize;
  avatarDataUrl?: string;
  avatarIcon?: string;
};

function AgentAvatar({
  className,
  color = "agent-1",
  initials,
  name,
  size = "md",
  avatarDataUrl,
  avatarIcon,
  ...props
}: AgentAvatarProps) {
  if (avatarDataUrl) {
    return (
      <div
        role="img"
        aria-label={name}
        className={cn(
          "inline-flex shrink-0 items-center justify-center overflow-hidden bg-muted",
          avatarSizeClassNames[size],
          className,
        )}
        {...props}
      >
        <img
          src={avatarDataUrl}
          alt={name}
          className="size-full object-cover"
          draggable={false}
        />
      </div>
    );
  }
  if (avatarIcon && hasIcon(avatarIcon)) {
    const Icon = iconForKey(avatarIcon);
    return (
      <div
        role="img"
        aria-label={name}
        className={cn(
          "inline-flex shrink-0 items-center justify-center text-white",
          avatarSizeClassNames[size],
          agentColorClassNames[color],
          className,
        )}
        {...props}
      >
        {React.createElement(Icon, {
          className: "size-[60%]",
          "aria-hidden": true,
        })}
      </div>
    );
  }
  return (
    <div
      role="img"
      aria-label={name}
      className={cn(
        "inline-flex shrink-0 items-center justify-center font-semibold text-white",
        avatarSizeClassNames[size],
        agentColorClassNames[color],
        className,
      )}
      {...props}
    >
      {initials ?? getInitials(name)}
    </div>
  );
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
  icon: SidebarIcon;
  label: string;
};

function SidebarButton({
  active = false,
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
