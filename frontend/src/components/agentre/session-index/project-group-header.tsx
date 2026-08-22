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
import {
  ProjectGroupHeader as UiProjectGroupHeader,
  groupActionRevealClassName,
} from "@agentre-ai/agentre-ui";

import { cn } from "@/lib/utils";
import { useChatAgentsStore } from "@/stores/chat-agents-store";

import type { AgentStatus } from "@/stores/types";

import { AgentAvatar, agentIconNode } from "../primitives";
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
  /**
   * 组头那枚记号的档位：子树里最强的一档（`error > waiting > running`，
   * 见 `strongestAttentionTone`）。`null` = 子树里没有需要关注的会话。
   *
   * **不是** `hasRunning` —— 计数统计的是全部 attention 条数，此前记号却写死绿色，
   * 于是 3 条未读的项目显示绿色「3」，而那三行自己画的是琥珀点。
   */
  attentionTone: AgentStatus | null;
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
  attentionTone,
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
  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        {/* 组头的形（chevron / 字形那一格 / 名字的字号阶梯 / attention 记号）已经
            归共享包（规格 2026-08-22「组头归一」）：此前它与随手对话组头、机器组头、
            agentre-server 那一份各画各的，同一条设计长出四种尺码。这里只剩宿主自己的
            东西 —— 未配置角标、六个动作、拖拽把手。 */}
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
          // R10：全部未配置时逐行角标撤掉，改由名字变灰 + 树顶那一条整体说明来承担；
          // 只有一部分未配置时反过来 —— 角标已经说清楚了，名字不再变灰。
          labelMuted={project.localPathMissing && allLocalPathsMissing}
          className={
            dragListeners ? "cursor-grab active:cursor-grabbing" : undefined
          }
          {...(dragListeners ?? {})}
          badges={
            project.localPathMissing && !allLocalPathsMissing ? (
              <span
                data-testid="project-local-path-missing-badge"
                className="inline-flex shrink-0 items-center rounded-sm border border-border px-1.5 py-0.5 text-2xs font-medium text-muted-foreground"
              >
                {t("projects.localPath.badge")}
              </span>
            ) : null
          }
          actions={
            <>
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
                    className={cn(
                      "inline-flex size-5 shrink-0 cursor-pointer items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground",
                      groupActionRevealClassName,
                    )}
                  >
                    <MoreVertical className="size-3" aria-hidden="true" />
                  </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem onSelect={() => onOpenSettings(project.id)}>
                    <Settings className="size-3.5" aria-hidden="true" />
                    {t("projectSettings.title")}
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    onSelect={() => onAddSubProject(project.id)}
                  >
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
                      <DropdownMenuItem
                        onSelect={() => onSpecifyPath(project.id)}
                      >
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
            </>
          }
        />
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
          className={cn(
            "inline-flex size-5 shrink-0 cursor-pointer items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground",
            groupActionRevealClassName,
          )}
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
        <div className="px-2 py-1.5 font-mono text-2xs uppercase tracking-wider text-muted-foreground">
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
