// 设置页的目录:页 id 的取值域、导航分组、以及「还没做」的那几页。
//
// 纯数据与类型,没有 JSX —— 导航、页面外壳与「施工中」占位都从这一份读。

import {
  Bell,
  Cable,
  Cpu,
  Database,
  FileText,
  Info,
  Keyboard,
  Network,
  RefreshCw,
  Server,
  Sparkles,
  SunMoon,
  Wrench,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";

export type SettingsNavSection = {
  labelKey: string;
  items: {
    id?: SettingsPageId;
    icon: LucideIcon;
    labelKey: string;
  }[];
};

export type SettingsPageId =
  | "agent-backend"
  | "appearance"
  | "remote-devices"
  | "data-backup"
  | "files"
  | "keyboard-shortcuts"
  | "llm-providers"
  | "local-proxy"
  | "mcp-servers"
  | "notifications"
  | "skills-tools"
  | "sync"
  | "version-logs";

export const settingsPageIds = new Set<SettingsPageId>([
  "agent-backend",
  "appearance",
  "remote-devices",
  "data-backup",
  "files",
  "keyboard-shortcuts",
  "llm-providers",
  "local-proxy",
  "mcp-servers",
  "notifications",
  "skills-tools",
  "sync",
  "version-logs",
]);

export function isSettingsPageId(value: unknown): value is SettingsPageId {
  return (
    typeof value === "string" && settingsPageIds.has(value as SettingsPageId)
  );
}

export const settingsNavSections: SettingsNavSection[] = [
  {
    labelKey: "settings.nav.general",
    items: [
      { icon: SunMoon, id: "appearance", labelKey: "settings.nav.appearance" },
      {
        icon: Bell,
        id: "notifications",
        labelKey: "settings.nav.notifications",
      },
      { icon: FileText, id: "files", labelKey: "settings.nav.files" },
      {
        icon: Keyboard,
        id: "keyboard-shortcuts",
        labelKey: "settings.nav.keyboardShortcuts",
      },
      {
        icon: Database,
        id: "data-backup",
        labelKey: "settings.nav.dataBackup",
      },
    ],
  },
  {
    labelKey: "settings.nav.engine",
    items: [
      {
        icon: Sparkles,
        id: "llm-providers",
        labelKey: "settings.nav.llmProvider",
      },
      { icon: Cpu, id: "agent-backend", labelKey: "settings.nav.agentBackend" },
    ],
  },
  {
    labelKey: "settings.nav.integrations",
    items: [
      { icon: Network, id: "local-proxy", labelKey: "settings.nav.localProxy" },
      { icon: Server, id: "mcp-servers", labelKey: "settings.nav.mcpServers" },
      {
        icon: Wrench,
        id: "skills-tools",
        labelKey: "settings.nav.skillsTools",
      },
      {
        icon: Cable,
        id: "remote-devices",
        labelKey: "settings.nav.remoteDevices",
      },
      { icon: RefreshCw, id: "sync", labelKey: "settings.nav.sync" },
    ],
  },
  {
    labelKey: "settings.nav.about",
    items: [
      { icon: Info, id: "version-logs", labelKey: "settings.nav.versionLogs" },
    ],
  },
];

export const underConstructionSettingsPages: Record<
  Exclude<
    SettingsPageId,
    | "agent-backend"
    | "appearance"
    | "remote-devices"
    | "keyboard-shortcuts"
    | "llm-providers"
    | "local-proxy"
    | "version-logs"
    | "data-backup"
    | "files"
    | "notifications"
    | "skills-tools"
    | "sync"
  >,
  {
    descriptionKey: string;
    icon: LucideIcon;
    titleKey: string;
  }
> = {
  "mcp-servers": {
    titleKey: "settings.underConstruction.mcpServers.title",
    descriptionKey: "settings.underConstruction.mcpServers.description",
    icon: Server,
  },
};

export const compactSettingsNavItems = settingsNavSections
  .flatMap((section) => section.items)
  .filter((item): item is typeof item & { id: SettingsPageId } =>
    Boolean(item.id),
  );
