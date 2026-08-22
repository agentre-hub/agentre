/**
 * 桌面端这一侧的项目设置 —— **只剩 adapter**（规格 2026-08-22 B 段，决策 2/3/4/7/14）。
 *
 * 弹窗本身住在 `@agentre-ai/agentre-ui` 里，两端同一份。这里做的只有一件事：把 wails
 * 那几个绑定与桌面端的数据形状（数字 `id`、`ProjectDetailResponse`、本地那张位置表）
 * 翻成包认识的 view + `ProjectSettingsPorts`。
 *
 * 换掉的是四个标签页（基本 / 成员 / 位置 / 危险）与两种保存语义。「危险」页随之消失，
 * 删除入口只剩组头 ⋮（决策 14）—— 此前两处都有，去掉其一不减少能力。
 *
 * **本机那一行**：它是这张表里唯一由宿主写自己的一行（`ProjectSetLocalPath`），
 * 所以不看在线状态；挑目录走系统原生对话框（`SelectDirectory`），比任何自绘面板都好。
 */
import * as React from "react";
import { useTranslation } from "react-i18next";

import { Input } from "@/components/ui/input";
import { useChatAgents } from "@/hooks/use-chat-agents";
import {
  ProjectSettingsDialog,
  type ProjectCandidateView,
  type ProjectMachineView,
  type ProjectMemberView,
  type ProjectSettingsPorts,
  type ProjectWriteOutcome,
} from "@agentre-ai/agentre-ui";

import {
  ProjectAddMember,
  ProjectGet,
  ProjectLocationList,
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

  const reload = React.useCallback(async () => {
    if (projectID <= 0) return;
    try {
      setDetail(await ProjectGet(projectID));
    } catch {
      setDetail(null);
    }
  }, [projectID]);

  React.useEffect(() => {
    if (open) void reload();
    else setDetail(null);
  }, [open, reload]);

  const project = detail?.project;

  const ports = React.useMemo<ProjectSettingsPorts>(
    () => ({
      updateFields: (_id, fields) =>
        attempt(async () => {
          const p = project;
          if (!p) return;
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
    [project, reload, t],
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
        // 桌面端改不了父项目：`ProjectUpdateRequest` 没有 parentID，而
        // `ProjectReorder` 的 SQL 带 `AND parent_id = ?`（只在同一个父下排序）。
        // 递空候选，包会把那一格整格不画。
        parentId: "",
        members,
        candidates,
      }}
      parentOptions={[]}
      ports={ports}
      focus={focus}
      iconField={({ value, onPick }) => (
        <IconKeyField value={value} onPick={onPick} />
      )}
      onChanged={() => {
        void reload();
        onChanged();
      }}
    />
  );
}

/**
 * 图标那一格由宿主画：桌面端今天收的是一个自由 icon key（`folder` / `briefcase` /
 * emoji 都行），那张 key → 图标的注册表是它自己的。写归包 —— 这里只在 blur 时把新值
 * 递回去，值没变不递。
 */
function IconKeyField({
  value,
  onPick,
}: {
  value: string;
  onPick: (iconKey: string) => void;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = React.useState(value);
  React.useEffect(() => setDraft(value), [value]);
  return (
    <label className="block text-xs font-medium text-foreground">
      {t("projectSettings.basic.iconKey")}
      <Input
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={() => {
          if (draft !== value) onPick(draft);
        }}
        placeholder={t("projectSettings.basic.iconPlaceholder")}
        className="mt-1 h-9 font-mono text-xs"
      />
    </label>
  );
}

export { ProjectSettingsDrawer };
