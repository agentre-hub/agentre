/**
 * 统一会话索引「按项目」档的项目组头 —— **只剩宿主自己的东西**。
 *
 * 组头的形（chevron / 字形那一格 / 名字的字号阶梯 / attention 记号）在 2026-08-22
 * 「组头归一」那一轮进了共享包；三样动作（＋ / ⋮ / 右键）在本轮（规格 2026-08-22
 * C 段）也进了包 —— 条目全集、顺序、分隔线位置、危险项样式只定义一次，⋮ 与右键
 * 各渲染一遍。合之前这两处各摆一份，右键那份就少了「成员…」「机器与路径…」。
 *
 * 这里剩下的只有：未配置角标要不要画（R10 的两档规则是宿主的）、能力声明、拖拽把手，
 * 以及「新建终端」那个子菜单 —— 机器清单与它怎么拉是宿主的事，包收的是 render prop。
 */
import * as React from "react";
import { TerminalSquare } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
} from "@/components/ui/context-menu";
import {
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
} from "@/components/ui/dropdown-menu";
import {
  ProjectGroupHeader as UiProjectGroupHeader,
  ProjectHeaderActions,
  ProjectHeaderContextMenu,
  type ProjectHeaderActionsProps,
  type ProjectHeaderMember,
} from "@agentre-ai/agentre-ui";

import { useChatAgentsStore } from "@/stores/chat-agents-store";

import type { AgentStatus } from "@/stores/types";

import { agentIconNode } from "../primitives";
import { useRemoteDevices } from "../remote-devices/use-remote-devices";
import * as WailsApp from "../../../../wailsjs/go/app/App";
import type { app } from "../../../../wailsjs/go/models";

type ProjectMemberItem = app.ProjectMemberItem & {
  agentName?: string;
  avatarColor?: string;
  avatarIcon?: string;
  avatarDataUrl?: string;
};

export type ProjectGroupHeaderProps = {
  project: app.ProjectItem;
  /** 0 = 根；头像尺寸与字号分 depth 0 / 1 / ≥2 三档。 */
  depth: number;
  expanded: boolean;
  onToggle: () => void;
  /** 组头上的绿点计数（含子项目）。 */
  attentionCount: number;
  /**
   * 组头那枚记号的档位：子树里最强的一档（`error > waiting > running`）。
   * `null` = 子树里没有需要关注的会话。
   */
  attentionTone: AgentStatus | null;
  /** 全部项目都缺本机路径时逐行角标撤掉，改由项目名变灰承担。 */
  allLocalPathsMissing: boolean;
  /** dnd-kit listeners；undefined = 当前不可拖。 */
  dragListeners?: Record<string, unknown>;
  onOpenSettings: (projectID: number, focus?: "members" | "paths") => void;
  onAddSubProject: (parentID: number) => void;
  onNewSession: (projectID: number, agentID: number) => void;
  onOpenTerminal: (
    projectID: number,
    deviceID: string,
    deviceName: string,
  ) => void;
  onMergeInto: (projectID: number, name: string) => void;
  onDelete: (projectID: number, name: string) => void;
};

export function ProjectGroupHeader({
  project,
  depth,
  expanded,
  onToggle,
  attentionCount,
  attentionTone,
  allLocalPathsMissing,
  dragListeners,
  onOpenSettings,
  onAddSubProject,
  onNewSession,
  onOpenTerminal,
  onMergeInto,
  onDelete,
}: ProjectGroupHeaderProps) {
  const { t } = useTranslation();
  const agents = useChatAgentsStore((s) => s.agents);

  /*
    成员在**浮层打开之前**取出来：恰好一个成员时包会直接开对话、不弹浮层。
    此前这一段是「先弹出来、拉回来发现只有一个、再自己关掉」，中间闪一下。
  */
  const loadMembers = React.useCallback(
    async (projectID: string): Promise<ProjectHeaderMember[]> => {
      const detail = await WailsApp.ProjectGet(Number(projectID));
      const byID = new Map(agents.map((a) => [a.id, a]));
      const rows = [
        ...((detail.directMembers ?? []) as ProjectMemberItem[]).map((m) => ({
          m,
          inherited: false,
        })),
        ...((detail.inheritedMembers ?? []) as ProjectMemberItem[]).map((m) => ({
          m,
          inherited: true,
        })),
      ];
      return rows.map(({ m, inherited }) => {
        const agent = byID.get(m.agentID);
        const name = m.agentName || agent?.name || `Agent #${m.agentID}`;
        return {
          id: String(m.agentID),
          name,
          color: m.avatarColor || agent?.avatarColor,
          avatarIcon: agentIconNode(m.avatarIcon || agent?.avatarIcon || ""),
          avatarDataUrl: m.avatarDataUrl || agent?.avatarDataUrl,
          inherited,
        };
      });
    },
    [agents],
  );

  const terminalSubmenu = React.useCallback(
    (kind: "dropdown" | "context") => (
      <TerminalSubMenu
        key="terminal"
        kind={kind}
        projectID={project.id}
        onPick={(deviceID, deviceName) =>
          onOpenTerminal(project.id, deviceID, deviceName ?? "")
        }
      />
    ),
    [onOpenTerminal, project.id],
  );

  const actions: ProjectHeaderActionsProps = {
    projectId: String(project.id),
    projectName: project.name,
    // R10 的两档规则是宿主的：全部未配置时逐行角标撤掉，改由名字变灰 + 树顶那条
    // 整体说明承担；只有一部分未配置时反过来。
    unconfigured: project.localPathMissing && !allLocalPathsMissing,
    capabilities: {
      terminal: true,
      // 「合并到已有」只对还没配本机路径的项目有意义 —— 它是 R10 的一条出路，
      // 不是一个随时可用的动作。能力是逐项目声明的，所以在这里判。
      merge: project.localPathMissing,
    },
    terminalSubmenu,
    loadMembers,
    onNewChat: (projectID, agentID) =>
      onNewSession(Number(projectID), Number(agentID)),
    onOpenSettings: (projectID, focus) =>
      onOpenSettings(Number(projectID), focus),
    onNewSubproject: (projectID) => onAddSubProject(Number(projectID)),
    onNewTerminal: () => onOpenTerminal(project.id, "", ""),
    onMergeInto: (projectID) => onMergeInto(Number(projectID), project.name),
    onDelete: (projectID) => onDelete(Number(projectID), project.name),
  };

  return (
    <ProjectHeaderContextMenu {...actions}>
      <UiProjectGroupHeader
        project={{ name: project.name, color: project.color }}
        glyph={agentIconNode(project.icon)}
        depth={depth}
        expanded={expanded}
        onToggle={onToggle}
        attentionCount={attentionCount}
        attentionTone={attentionTone}
        attentionTitle={t("projects.session.activeCount", {
          count: attentionCount,
        })}
        labelMuted={project.localPathMissing && allLocalPathsMissing}
        className={
          dragListeners ? "cursor-grab active:cursor-grabbing" : undefined
        }
        {...(dragListeners ?? {})}
        actions={<ProjectHeaderActions {...actions} />}
      />
    </ProjectHeaderContextMenu>
  );
}

// useProjectTerminalLocations —— 「新建终端」共享逻辑：懒加载该项目已配置的
// location，结合 device 在线 / 路径可用性。两种容器的子菜单共用同一套判定。
function useProjectTerminalLocations(projectID: number) {
  const { devices } = useRemoteDevices();
  const [configured, setConfigured] = React.useState<Set<string> | null>(null);
  const loadLocations = React.useCallback(() => {
    void WailsApp.ProjectLocationList(projectID).then((rows) =>
      setConfigured(new Set((rows ?? []).map((r) => r.deviceId))),
    );
  }, [projectID]);
  // 终端位置按 LAN 配对行的 id 索引（ProjectLocationList.deviceId），账号独有的
  // 那些机器没有配对行，也就没有可落位的 location，不进这个菜单。
  const lanDevices = React.useMemo(
    () => devices.flatMap((d) => (d.lan ? [d.lan] : [])),
    [devices],
  );
  return { devices: lanDevices, configured, loadLocations };
}

/**
 * 「新建终端」子菜单 —— 两种容器一份实现。
 *
 * 它留在宿主而不是进包（规格 Out of scope）：终端本身是桌面端独有的东西，包只在
 * 「条目全集」里认识这个概念。但两种容器的条目组件不是同一个，所以这里按 `kind`
 * 选一套构件 —— 判定逻辑（在线 + 配了路径才可选）只有一份。
 */
function TerminalSubMenu({
  kind,
  projectID,
  onPick,
}: {
  kind: "dropdown" | "context";
  projectID: number;
  onPick: (deviceID: string, deviceName?: string) => void;
}) {
  const { t } = useTranslation();
  const { devices, configured, loadLocations } =
    useProjectTerminalLocations(projectID);
  const Sub = kind === "dropdown" ? DropdownMenuSub : ContextMenuSub;
  const SubTrigger =
    kind === "dropdown" ? DropdownMenuSubTrigger : ContextMenuSubTrigger;
  const SubContent =
    kind === "dropdown" ? DropdownMenuSubContent : ContextMenuSubContent;
  const Item = kind === "dropdown" ? DropdownMenuItem : ContextMenuItem;
  const Separator =
    kind === "dropdown" ? DropdownMenuSeparator : ContextMenuSeparator;

  return (
    <Sub
      onOpenChange={(open: boolean) => {
        if (open && configured === null) loadLocations();
      }}
    >
      <SubTrigger data-testid="project-menu-item-terminal">
        <TerminalSquare className="size-3.5" aria-hidden="true" />
        {t("projects.terminal.new")}
      </SubTrigger>
      <SubContent>
        <Item onSelect={() => onPick("", undefined)}>
          {t("projects.terminal.local")}
        </Item>
        {devices.length > 0 ? <Separator /> : null}
        {devices.map((d) => {
          const id = String(d.id);
          const hasPath = configured?.has(id) ?? false;
          const disabled = !d.online || !hasPath;
          return (
            <Item
              key={id}
              disabled={disabled}
              title={
                !d.online
                  ? t("projects.terminal.deviceOffline")
                  : !hasPath
                    ? t("projects.terminal.pathNotConfigured")
                    : undefined
              }
              onSelect={() => {
                if (!disabled) onPick(id, d.name);
              }}
            >
              {d.name}
              {!d.online
                ? t("projects.terminal.offlineSuffix")
                : !hasPath
                  ? t("projects.terminal.pathMissingSuffix")
                  : ""}
            </Item>
          );
        })}
      </SubContent>
    </Sub>
  );
}
