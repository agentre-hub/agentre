/**
 * 桌面端这一侧的项目设置 —— **只剩 adapter**（规格 2026-08-22 B 段，决策 2/3/4/7/14）。
 *
 * 弹窗本身住在 `@agentre-hub/agentre-ui` 里，两端同一份。这里做的只有一件事：把 wails
 * 那几个绑定与桌面端的数据形状（数字 `id`、`ProjectDetailResponse`、本地那张位置表）
 * 翻成包认识的 view + `ProjectSettingsPorts`。
 *
 * 换掉的是四个标签页（基本 / 成员 / 位置 / 危险）与两种保存语义。「危险」页随之消失，
 * 删除入口只剩组头 ⋮（决策 14）—— 此前两处都有，去掉其一不减少能力。
 *
 * **本机那一行**：它是这张表里唯一由宿主写自己的一行（`ProjectSetLocalPath`），
 * 所以不看在线状态；挑目录走系统原生对话框（`SelectDirectory`），比任何自绘面板都好。
 *
 * **图标与颜色这一端不再插手**（2026-08-27）：那张 key → 图标的词表本来就住在共享包
 * 里（`org/icon-registry`，两端同一份），字形选择器于是也归包。此前这里挂的是一个要
 * 人手打 key 的输入框，placeholder 写着「folder / briefcase / 自定义 emoji」——而
 * 侧栏的字形走 `hasIcon(key)` 判定，那三种值一个都画不出来。
 */
import * as React from "react";
import { useTranslation } from "react-i18next";

import {
  ProjectSettingsDialog,
  type ProjectCandidateView,
  type ProjectMachineView,
  type ProjectMemberView,
  type ProjectSettingsPorts,
  type ProjectWriteOutcome,
} from "@agentre-hub/agentre-ui";
import { useChatAgents } from "@/hooks/use-chat-agents";

import {
  ProjectAddMember,
  ProjectGet,
  ProjectListTree,
  ProjectLocationList,
  ProjectMove,
  ProjectLocationRemove,
  ProjectLocationUpsert,
  ProjectRemoveMember,
  ProjectSetLocalPath,
  ProjectUpdate,
  RemoteDeviceList,
  SelectDirectory,
} from "../../../wailsjs/go/app/App";
import type { app } from "../../../wailsjs/go/models";
import { createRemoteFsPort } from "./remote-fs-port";

type ProjectDetailResponse = app.ProjectDetailResponse;
type ProjectMemberItem = app.ProjectMemberItem & {
  agentName?: string;
  avatarColor?: string;
  avatarIcon?: string;
  avatarDataUrl?: string;
};

// 这两个 view 由 wailsjs codegen 在 `make dev` 时刷新；在此之前保住 TS 安全。
type ProjectLocationView = {
  deviceId: string;
  path: string;
  deviceName: string;
  online: boolean;
};
type DeviceView = { id: number; name: string; online: boolean };

/** 项目树拍平成父项目候选。深度只影响缩进，这一格暂不缩进。 */
function flattenTree(
  nodes: app.ProjectTreeNode[],
): { id: string; name: string }[] {
  const out: { id: string; name: string }[] = [];
  for (const n of nodes) {
    if (!n.project) continue;
    out.push({ id: String(n.project.id), name: n.project.name });
    if (n.children) out.push(...flattenTree(n.children));
  }
  return out;
}

export type ProjectSettingsDrawerProps = {
  /** 0 = 关闭；>0 = 打开并加载该项目 */
  projectID: number;
  /** 直落到哪一节；组头菜单的「成员…」「机器与路径…」与「未配置」角标经它进来。 */
  focus?: "members" | "paths";
  onClose: () => void;
  onChanged: () => void;
};

/** 无状态，建一次就够——每次渲染新建一个会让选择器那个 effect 每帧重跑。 */
const fsPort = createRemoteFsPort();

/** 本机那一行的 id。空串在这一端不是任何一台远端设备的 id，拿来当哨兵是安全的。 */
const SELF_ID = "";

/**
 * 桌面端分不出写失败是哪一类：Go 那侧的错误跨 wails 只剩一句文本，没有码可读
 * （与 `remote-fs-port.ts` 同一处缺口）。所以一律交 `unknown` 并把原文带上 ——
 * 包会原样透出它，用户看到的仍是 Go 那句人话。
 */
async function attempt(
  run: () => Promise<unknown>,
): Promise<ProjectWriteOutcome> {
  try {
    await run();
    return { ok: true };
  } catch (e) {
    return { ok: false, failure: { kind: "unknown", message: String(e) } };
  }
}

function ProjectSettingsDrawer({
  projectID,
  focus,
  onClose,
  onChanged,
}: ProjectSettingsDrawerProps) {
  const { t } = useTranslation();
  const open = projectID > 0;
  const { agents } = useChatAgents();
  const [detail, setDetail] = React.useState<ProjectDetailResponse | null>(
    null,
  );
  /** 候选里「这台设备还没配路径」那一档要看它，所以设置弹窗这一层就得知道。 */
  const [locations, setLocations] = React.useState<ProjectLocationView[]>([]);
  /** 父项目下拉的候选。整棵树拍平，包会把「它自己」剔掉。 */
  const [tree, setTree] = React.useState<{ id: string; name: string }[]>([]);

  const reload = React.useCallback(async () => {
    if (projectID <= 0) return;
    try {
      setDetail(await ProjectGet(projectID));
    } catch {
      setDetail(null);
    }
  }, [projectID]);

  React.useEffect(() => {
    if (!open) {
      setDetail(null);
      return;
    }
    void reload();
    void ProjectListTree()
      .then((nodes) => setTree(flattenTree(nodes ?? [])))
      // 读不到就不画那一格 —— 画一个空下拉比不画更让人以为「没有别的项目」。
      .catch(() => setTree([]));
  }, [open, reload]);

  const project = detail?.project;

  /**
   * 这个项目的成员分别绑在哪几台机器上。
   *
   * 「机器与路径」那一节回答的是「这个项目在哪」，所以包只直接列出本机、已配路径的、
   * 以及**有成员在上面的**那几台；账号里其余的收进那颗「＋」。这一档由宿主答，因为
   * 只有宿主知道一个 Agent 绑在哪台设备上（`useChatAgents` 那一侧的 `deviceID`）。
   *
   * 先折成一个字符串键再建集合：`detail` 每次重取都是新对象，直接进 `useMemo` 的
   * 依赖会让下面那份 ports 每写一次就换一次身份，包那边的机器清单也就跟着白取一遍。
   */
  const memberAgentIDs = React.useMemo(() => {
    const ids = [
      ...(detail?.directMembers ?? []),
      ...(detail?.inheritedMembers ?? []),
    ].map((m) => m.agentID);
    return Array.from(new Set(ids)).sort().join(",");
  }, [detail]);
  const memberDeviceIDs = React.useMemo(() => {
    const wanted = new Set(memberAgentIDs ? memberAgentIDs.split(",") : []);
    const out = new Set<string>();
    for (const a of agents) {
      if (!wanted.has(String(a.id))) continue;
      const deviceID = (a as { deviceID?: string }).deviceID;
      if (deviceID) out.add(deviceID);
    }
    return out;
  }, [agents, memberAgentIDs]);

  const ports = React.useMemo<ProjectSettingsPorts>(
    () => ({
      updateFields: (_id, fields) =>
        attempt(async () => {
          const p = project;
          if (!p) return;
          // 换父项目走 ProjectMove，不走 ProjectUpdate：那一条只管重名，换一层还要
          // 管父级在不在、停没停用、以及会不会成环（project_svc.Move）。
          if (fields.parentId !== undefined) {
            await ProjectMove({
              id: p.id,
              parentID: Number(fields.parentId || 0),
            });
            await reload();
            return;
          }
          // 桌面端的 ProjectUpdate 收整份字段，包只递改动的那几格 —— 在这里合。
          if (
            "name" in fields ||
            "icon" in fields ||
            "color" in fields ||
            "description" in fields
          ) {
            await ProjectUpdate({
              id: p.id,
              name: fields.name ?? p.name,
              icon: fields.icon ?? p.icon,
              color: fields.color ?? p.color,
              description: fields.description ?? p.description,
            });
          }
          await reload();
        }),
      addMember: (_id, candidateId) =>
        attempt(async () => {
          if (!project) return;
          await ProjectAddMember(project.id, Number(candidateId));
          await reload();
        }),
      removeMember: (_id, member) =>
        attempt(async () => {
          if (!project) return;
          await ProjectRemoveMember(project.id, Number(member.id));
          await reload();
        }),
      listMachines: async () => {
        if (!project) return [];
        const [locs, devices] = await Promise.all([
          ProjectLocationList(project.id) as Promise<ProjectLocationView[]>,
          RemoteDeviceList() as Promise<DeviceView[]>,
        ]);
        setLocations(locs ?? []);
        const byDevice = new Map(
          (locs ?? []).map((l) => [l.deviceId, l] as const),
        );
        return [
          {
            id: SELF_ID,
            // 桌面端没有一个说得出自己叫什么的绑定；包会用它自己的「本机」补上。
            name: "",
            kind: "desktop" as const,
            online: true,
            isSelf: true,
            // 宿主写自己，不经中继 —— 与在线无关。
            writeNeedsOnline: false,
            path: project.path,
            removable: !!project.path,
          },
          ...(devices ?? []).map((d): ProjectMachineView => {
            const id = String(d.id);
            return {
              id,
              name: d.name,
              kind: "agentred" as const,
              online: d.online,
              // 位置表住在本机的库里，`ProjectLocationUpsert` 不经那台机器 ——
              // 所以离线也配得了；离线只挡住「浏览它的目录」。
              writeNeedsOnline: false,
              path: byDevice.get(id)?.path ?? "",
              removable: byDevice.has(id),
              hasMember: memberDeviceIDs.has(id),
            };
          }),
        ];
      },
      setMachinePath: (_id, machine, path) =>
        attempt(async () => {
          if (!project) return;
          if (machine.isSelf) {
            await ProjectSetLocalPath({ id: project.id, path });
          } else {
            await ProjectLocationUpsert(project.id, machine.id, path);
          }
          await reload();
        }),
      clearMachinePath: (_id, machine) =>
        attempt(async () => {
          if (!project) return;
          if (machine.isSelf) {
            await ProjectSetLocalPath({ id: project.id, path: "" });
          } else {
            await ProjectLocationRemove(project.id, machine.id);
          }
          await reload();
        }),
      fs: fsPort,
      pickLocalDirectory: async () =>
        (await SelectDirectory(t("projectSettings.basic.localPath"))) || null,
    }),
    [memberDeviceIDs, project, reload, t],
  );

  if (!open || !project) return null;

  const direct = (detail?.directMembers ?? []) as ProjectMemberItem[];
  const inherited = (detail?.inheritedMembers ?? []) as ProjectMemberItem[];
  const memberIDs = new Set([
    ...direct.map((m) => m.agentID),
    ...inherited.map((m) => m.agentID),
  ]);
  const configuredDevices = new Set(locations.map((l) => l.deviceId));

  const members: ProjectMemberView[] = [
    ...direct.map((m) => ({
      id: String(m.agentID),
      name: m.agentName || `Agent #${m.agentID}`,
      color: m.avatarColor,
      avatarDataUrl: m.avatarDataUrl,
    })),
    ...inherited.map((m) => ({
      // 继承来的与直接成员可能是同一个 Agent，键要能分得开。
      id: `inherited-${m.agentID}`,
      name: m.agentName || `Agent #${m.agentID}`,
      color: m.avatarColor,
      avatarDataUrl: m.avatarDataUrl,
      inherited: true,
      inheritedFrom: m.fromName,
    })),
  ];

  const candidates: ProjectCandidateView[] = agents
    .filter((a) => !memberIDs.has(a.id))
    .map((a): ProjectCandidateView => {
      const deviceID = (a as { deviceID?: string }).deviceID;
      // 远端 Agent 要先给它那台设备配路径，否则加进来也开不出对话（cwd 解不出来）。
      const blocked = !!deviceID && !configuredDevices.has(deviceID);
      return {
        id: String(a.id),
        name: a.name,
        color: a.avatarColor,
        avatarDataUrl: a.avatarDataUrl,
        disabled: blocked,
        disabledReason: blocked
          ? t("projectSettings.members.configureRemotePath")
          : undefined,
      };
    });

  return (
    <ProjectSettingsDialog
      open
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
      project={{
        id: String(project.id),
        name: project.name,
        description: project.description,
        icon: project.icon,
        color: project.color,
        parentId: String(project.parentID || ""),
        members,
        candidates,
      }}
      parentOptions={tree}
      ports={ports}
      focus={focus}
      onChanged={() => {
        void reload();
        onChanged();
      }}
    />
  );
}

export { ProjectSettingsDrawer };
