// project-group-header.tsx —— 统一会话索引「按项目」档的项目组头。
//
// 从 project-page.tsx 的 ProjectCard.renderHeader 原样提取（规格
// docs/specs/2026-08-16-unified-chat-index.md「组头保留清单」）：折叠箭头 +
// 项目头像 + 名称 + attention 计数 + 未配置角标 + ＋（成员 agent picker）+
// ⋮ 六项 + 右键四项，以及运行中的品牌底色与左侧 3px 绿条。
//
// 与原实现的唯一区别是不再吃 ProjectCard 的 selection/drag 内部结构：宿主索引
// 侧栏把每个动作以回调形式传进来，拖拽只交 dnd-kit 的 listeners（整行即把手）。
import * as React from "react";
import {
  ChevronDown,
  FolderCog,
  GitMerge,
  MoreVertical,
  Plus,
  Settings,
  TerminalSquare,
  Trash2,
} from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";
import { useChatAgentsStore } from "@/stores/chat-agents-store";

import { AgentAvatar } from "../primitives";
import { useRemoteDevices } from "../remote-devices/use-remote-devices";
import type { AgentColor } from "../types";
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
  /** 子树有 running → 品牌底色 + 左侧 3px 绿条。 */
  hasRunning: boolean;
  /** 全部项目都缺本机路径时逐行角标撤掉，改由项目名变灰承担。 */
  allLocalPathsMissing: boolean;
  /** dnd-kit listeners；undefined = 当前不可拖。 */
  dragListeners?: Record<string, unknown>;
  onOpenSettings: (projectID: number) => void;
  onAddSubProject: (parentID: number) => void;
  onNewSession: (projectID: number, agentID: number) => void;
  onOpenTerminal: (
    projectID: number,
    deviceID: string,
    deviceName: string,
  ) => void;
  onSpecifyPath: (projectID: number) => void;
  onMergeInto: (projectID: number, name: string) => void;
  onDelete: (projectID: number, name: string) => void;
};

export function ProjectGroupHeader({
  project,
  depth,
  expanded,
  onToggle,
  attentionCount,
  hasRunning,
  allLocalPathsMissing,
  dragListeners,
  onOpenSettings,
  onAddSubProject,
  onNewSession,
  onOpenTerminal,
  onSpecifyPath,
  onMergeInto,
  onDelete,
}: ProjectGroupHeaderProps) {
  const { t } = useTranslation();
  // depth > 0 时仅靠 pl-1 (4px) 给一点缩进；层级表达全部由这里的 UPPERCASE mono
  // section label + 字号 / 颜色差异承担 —— 不再用左竖线 / 大缩进。
  const isSub = depth > 0;
  const isDeep = depth >= 2;

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <div
          data-testid="project-group-header"
          className={cn(
            "group/proj flex items-center gap-1.5 rounded-md text-xs hover:bg-sidebar-active-bg",
            isSub ? "px-1.5 py-1" : "px-2 py-1.5",
            dragListeners && "cursor-grab active:cursor-grabbing",
            // R2x 运行中品牌高亮：brand-soft 底色 + 左侧 running 竖条。
            hasRunning && "bg-primary-soft relative",
          )}
          {...(dragListeners ?? {})}
        >
          {hasRunning ? (
            <span
              data-testid="project-running-indicator"
              aria-hidden="true"
              className="absolute left-0 top-1/2 h-[60%] w-[3px] -translate-y-1/2 rounded-full bg-status-running"
            />
          ) : null}
          <button
            type="button"
            className="flex min-w-0 flex-1 items-center gap-1.5 outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
            onClick={onToggle}
            aria-expanded={expanded}
          >
            <ChevronDown
              className={cn(
                "text-muted-foreground transition-transform duration-150 ease-out motion-reduce:transition-none",
                isSub ? "size-3" : "size-3.5",
                !expanded && "-rotate-90",
              )}
              aria-hidden="true"
            />
            <AgentAvatar
              name={project.name}
              initials={project.name.charAt(0)}
              color={(project.color as AgentColor) || "agent-1"}
              avatarIcon={project.icon || "folder"}
              size="sm"
              className={cn(
                isDeep
                  ? "size-3.5 rounded-sm"
                  : isSub
                    ? "size-4 rounded-sm"
                    : "size-6 rounded-md",
              )}
            />
            <span
              className={cn(
                "min-w-0 flex-1 truncate text-left",
                isDeep
                  ? "font-mono text-[9px] font-medium uppercase tracking-widest text-subtle-foreground"
                  : isSub
                    ? "font-mono text-2xs font-semibold uppercase tracking-wider text-muted-foreground"
                    : "text-[15px] font-semibold",
                // R10：全部未配置时逐行角标撤掉，改由名字变灰 + 树顶那一条整体
                // 说明来承担；只有一部分未配置时反过来——角标已经说清楚了，
                // 名字不再变灰。
                project.localPathMissing && allLocalPathsMissing
                  ? "text-muted-foreground"
                  : "",
              )}
            >
              {project.name}
            </span>
            {attentionCount > 0 ? (
              <span
                className="inline-flex items-center gap-1 font-mono text-2xs text-status-running"
                title={t("projects.session.activeCount", {
                  count: attentionCount,
                })}
              >
                <span
                  aria-hidden="true"
                  className="inline-block size-1.5 rounded-full bg-status-running"
                />
                {attentionCount}
              </span>
            ) : null}
            {project.localPathMissing && !allLocalPathsMissing ? (
              <span
                data-testid="project-local-path-missing-badge"
                className="inline-flex shrink-0 items-center rounded-sm border border-border px-1.5 py-0.5 text-2xs font-medium text-muted-foreground"
              >
                {t("projects.localPath.badge")}
              </span>
            ) : null}
          </button>
          <NewSessionMenu
            project={project}
            onPick={(agentID) => onNewSession(project.id, agentID)}
          />
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                aria-label={t("projects.actions.more", {
                  name: project.name,
                })}
                className="inline-flex size-5 shrink-0 cursor-pointer items-center justify-center rounded text-muted-foreground opacity-0 transition-opacity hover:bg-accent hover:text-foreground group-hover/proj:opacity-100 focus:opacity-100 focus-visible:opacity-100"
              >
                <MoreVertical className="size-3" aria-hidden="true" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onSelect={() => onOpenSettings(project.id)}>
                <Settings className="size-3.5" aria-hidden="true" />
                {t("projectSettings.title")}
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => onAddSubProject(project.id)}>
                <Plus className="size-3.5" aria-hidden="true" />
                {t("projects.actions.newSubProject")}
              </DropdownMenuItem>
              <NewTerminalSubMenu
                projectID={project.id}
                onPick={(deviceID, deviceName) =>
                  onOpenTerminal(project.id, deviceID, deviceName ?? "")
                }
              />
              {project.localPathMissing ? (
                <>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem onSelect={() => onSpecifyPath(project.id)}>
                    <FolderCog className="size-3.5" aria-hidden="true" />
                    {t("projects.localPath.specifyPath")}
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    onSelect={() => onMergeInto(project.id, project.name)}
                  >
                    <GitMerge className="size-3.5" aria-hidden="true" />
                    {t("projects.localPath.mergeIntoExisting")}
                  </DropdownMenuItem>
                </>
              ) : null}
              <DropdownMenuSeparator />
              <DropdownMenuItem
                variant="destructive"
                onSelect={() => onDelete(project.id, project.name)}
              >
                <Trash2 className="size-3.5" aria-hidden="true" />
                {t("projects.actions.deleteProject")}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent className="min-w-[180px]">
        <ContextMenuItem onSelect={() => onOpenSettings(project.id)}>
          <Settings className="size-3.5" aria-hidden="true" />
          {t("projectSettings.title")}
        </ContextMenuItem>
        <ContextMenuItem onSelect={() => onAddSubProject(project.id)}>
          <Plus className="size-3.5" aria-hidden="true" />
          {t("projects.actions.newSubProject")}
        </ContextMenuItem>
        <ProjectContextTerminalSubMenu
          projectID={project.id}
          onPick={(deviceID, deviceName) =>
            onOpenTerminal(project.id, deviceID, deviceName ?? "")
          }
        />
        <ContextMenuSeparator />
        <ContextMenuItem
          variant="destructive"
          onSelect={() => onDelete(project.id, project.name)}
        >
          <Trash2 className="size-3.5" aria-hidden="true" />
          {t("projects.actions.deleteProject")}
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

// NewSessionMenu —— 组头的"+"按钮 + 成员 agent 列表下拉。
// 弹出时 lazy 拉取该项目成员，渲染可选 agent 行（直属 + 继承），点击 → onPick。
type NewSessionMenuProps = {
  project: app.ProjectItem;
  onPick: (agentID: number) => void;
};

type MemberMenuLoadState =
  | { status: "idle"; projectID: number; members: ProjectMemberItem[] }
  | { status: "loading"; projectID: number; members: ProjectMemberItem[] }
  | { status: "loaded"; projectID: number; members: ProjectMemberItem[] }
  | {
      status: "error";
      projectID: number;
      members: ProjectMemberItem[];
      error: string;
    };

function NewSessionMenu({ project, onPick }: NewSessionMenuProps) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const [loadState, setLoadState] = React.useState<MemberMenuLoadState>({
    status: "idle",
    projectID: 0,
    members: [],
  });
  const agents = useChatAgentsStore((s) => s.agents);
  const agentByID = React.useMemo(
    () => new Map(agents.map((a) => [a.id, a])),
    [agents],
  );
  const handleOpenChange = React.useCallback(
    (nextOpen: boolean) => {
      setOpen(nextOpen);
      if (nextOpen) {
        setLoadState({
          status: "loading",
          projectID: project.id,
          members: [],
        });
      }
    },
    [project.id],
  );

  React.useEffect(() => {
    if (!open) return;
    let cancelled = false;
    void WailsApp.ProjectGet(project.id)
      .then((detail) => {
        if (cancelled) return;
        const members = [
          ...((detail.directMembers ?? []) as ProjectMemberItem[]),
          ...((detail.inheritedMembers ?? []) as ProjectMemberItem[]),
        ];
        if (members.length === 1) {
          onPick(members[0].agentID);
          setOpen(false);
          return;
        }
        setLoadState({
          status: "loaded",
          projectID: project.id,
          members,
        });
      })
      .catch((err) => {
        if (!cancelled) {
          setLoadState({
            status: "error",
            projectID: project.id,
            members: [],
            error: String(err),
          });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [onPick, open, project.id]);

  const activeLoadState =
    loadState.projectID === project.id
      ? loadState
      : { status: "loading" as const, projectID: project.id, members: [] };
  const members = activeLoadState.members;

  return (
    <DropdownMenu open={open} onOpenChange={handleOpenChange}>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={t("projects.session.newForProject", {
            name: project.name,
          })}
          title={t("projects.session.new")}
          className="inline-flex size-5 shrink-0 cursor-pointer items-center justify-center rounded text-muted-foreground opacity-0 transition-opacity hover:bg-accent hover:text-foreground group-hover/proj:opacity-100 focus:opacity-100 focus-visible:opacity-100"
        >
          <Plus className="size-3" aria-hidden="true" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="end"
        className="min-w-[220px]"
        // 阻止 Radix 默认把焦点还给 trigger —— 选完 agent 后新 tab 的输入框
        // 已经被 ChatPanelHost 接管，让 Radix 抢回 trigger 会直接抹掉那次 focus。
        onCloseAutoFocus={(e) => e.preventDefault()}
      >
        <div className="px-2 py-1.5 font-mono text-2xs uppercase tracking-wider text-subtle-foreground">
          {t("projects.session.pickAgent")}
        </div>
        {activeLoadState.status === "loading" ? (
          <div className="px-3 py-3 text-2xs text-muted-foreground">
            {t("projects.session.loadingMembers")}
          </div>
        ) : activeLoadState.status === "error" ? (
          <div className="px-3 py-3 text-2xs text-destructive">
            {t("projects.session.loadMembersFailed", {
              error: activeLoadState.error,
            })}
          </div>
        ) : members.length === 0 ? (
          <div className="px-3 py-3 text-2xs text-muted-foreground">
            {t("projects.session.noMembers")}
          </div>
        ) : (
          members.map((m) => {
            const agent = agentByID.get(m.agentID);
            const name = m.agentName || agent?.name || `Agent #${m.agentID}`;
            const avatarColor =
              (m.avatarColor as AgentColor) ||
              (agent?.avatarColor as AgentColor) ||
              "agent-1";
            const avatarIcon = m.avatarIcon || agent?.avatarIcon || undefined;
            const avatarDataUrl =
              m.avatarDataUrl || agent?.avatarDataUrl || undefined;
            return (
              <DropdownMenuItem
                key={`${m.inherited ? "i" : "d"}-${m.agentID}`}
                onSelect={() => {
                  onPick(m.agentID);
                  setOpen(false);
                }}
              >
                <AgentAvatar
                  name={name}
                  initials={name.charAt(0)}
                  color={avatarColor}
                  avatarIcon={avatarIcon}
                  avatarDataUrl={avatarDataUrl}
                  size="sm"
                />
                <span className="min-w-0 flex-1 truncate">{name}</span>
                {m.inherited ? (
                  <span className="rounded-sm bg-secondary px-1.5 py-0.5 text-2xs text-muted-foreground">
                    {t("projects.session.inherited")}
                  </span>
                ) : null}
              </DropdownMenuItem>
            );
          })
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// useProjectTerminalLocations —— 「新建终端」共享逻辑：懒加载该项目已配置的
// location，结合 device 在线 / 路径可用性。NewTerminalSubMenu（⋯ 菜单）与
// ProjectContextTerminalSubMenu（右键菜单）共用同一套判定。
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

// NewTerminalSubMenu —— 组头「更多操作」里的「新建终端」子菜单。
// 打开时 lazy 加载该项目已配置的 location，结合 device 在线状态决定可选性。
function NewTerminalSubMenu({
  projectID,
  onPick,
}: {
  projectID: number;
  onPick: (deviceID: string, deviceName?: string) => void;
}) {
  const { t } = useTranslation();
  const { devices, configured, loadLocations } =
    useProjectTerminalLocations(projectID);
  return (
    <DropdownMenuSub
      onOpenChange={(open) => {
        if (open && configured === null) loadLocations();
      }}
    >
      <DropdownMenuSubTrigger>
        <TerminalSquare className="size-3.5" aria-hidden="true" />
        {t("projects.terminal.new")}
      </DropdownMenuSubTrigger>
      <DropdownMenuSubContent>
        <DropdownMenuItem onSelect={() => onPick("", undefined)}>
          {t("projects.terminal.local")}
        </DropdownMenuItem>
        {devices.length > 0 ? <DropdownMenuSeparator /> : null}
        {devices.map((d) => {
          const id = String(d.id);
          const hasPath = configured?.has(id) ?? false;
          const disabled = !d.online || !hasPath;
          return (
            <DropdownMenuItem
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
            </DropdownMenuItem>
          );
        })}
      </DropdownMenuSubContent>
    </DropdownMenuSub>
  );
}

// ProjectContextTerminalSubMenu —— 组头右键 ContextMenu 里的「新建终端」子菜单。
// 行为与 NewTerminalSubMenu 一致（lazy 加载 ProjectLocationList + device 在线/路径）。
function ProjectContextTerminalSubMenu({
  projectID,
  onPick,
}: {
  projectID: number;
  onPick: (deviceID: string, deviceName?: string) => void;
}) {
  const { t } = useTranslation();
  const { devices, configured, loadLocations } =
    useProjectTerminalLocations(projectID);
  return (
    <ContextMenuSub
      onOpenChange={(open) => {
        if (open && configured === null) loadLocations();
      }}
    >
      <ContextMenuSubTrigger>
        <TerminalSquare className="size-3.5" aria-hidden="true" />
        {t("projects.terminal.new")}
      </ContextMenuSubTrigger>
      <ContextMenuSubContent>
        <ContextMenuItem onSelect={() => onPick("", undefined)}>
          {t("projects.terminal.local")}
        </ContextMenuItem>
        {devices.length > 0 ? <ContextMenuSeparator /> : null}
        {devices.map((d) => {
          const id = String(d.id);
          const hasPath = configured?.has(id) ?? false;
          const disabled = !d.online || !hasPath;
          return (
            <ContextMenuItem
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
            </ContextMenuItem>
          );
        })}
      </ContextMenuSubContent>
    </ContextMenuSub>
  );
}
