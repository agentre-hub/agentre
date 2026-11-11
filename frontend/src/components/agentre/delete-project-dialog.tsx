/**
 * 桌面端这一侧的「删除项目」确认 —— **只剩 adapter**（规格 2026-08-22 B 段，决策 8）。
 *
 * 弹窗本身住在 `@agentre-hub/agentre-ui` 里，两端同一份。这一端此前只说了一句笼统的
 * 后果；现在四件事逐条写清，而「输入完整项目名才放行」这条门槛本来就是这一端的，
 * 两端向它对齐。
 *
 * 「这棵子树下还有几个」与「哪几台机器此刻离线」由这里算出来递进去 —— 它们是宿主的
 * 数据，包不去猜。
 */
import * as React from "react";

import {
  ProjectDeleteDialog,
  type ProjectDeletePorts,
  type ProjectWriteFailure,
} from "@agentre-hub/agentre-ui";

import { classifyAppError } from "@/lib/app-error";

import {
  MemoryCountsByOwner,
  ProjectDelete,
  ProjectListTree,
  ProjectLocationList,
  RemoteDeviceList,
} from "../../../wailsjs/go/app/App";

export type DeleteProjectTarget = {
  id: number;
  name: string;
  /** 这棵子树下还有几个项目（不含它自己）；调用方从树上数。 */
  childCount?: number;
};

type ProjectTreeView = {
  project?: { id: number };
  children?: ProjectTreeView[];
};

/** 这棵项目树里 rootID 自己连同全部后代的 id；rootID 不在树里时只有它自己。 */
function subtreeIds(roots: ProjectTreeView[], rootID: number): Set<number> {
  const ids = new Set<number>([rootID]);
  const collect = (n: ProjectTreeView) => {
    if (n.project) ids.add(n.project.id);
    (n.children ?? []).forEach(collect);
  };
  const find = (nodes: ProjectTreeView[]): boolean => {
    for (const n of nodes) {
      if (n.project?.id === rootID) {
        collect(n);
        return true;
      }
      if (find(n.children ?? [])) return true;
    }
    return false;
  };
  find(roots);
  return ids;
}

type ProjectLocationView = { deviceId: string; path: string };
type DeviceView = { id: number; name: string; online: boolean };

/**
 * 过桥失败 → 共享弹窗认识的那一档（S5）。
 *
 * 带码时后端那句本地化文案就是事实，原样交进 `message`；不带码的是机器原话
 * （Go 包装错误），不交 —— 包会说它自己的「项目没删成」。共享弹窗没有「详情」位，
 * 原话因此不上界面。
 */
function toDeleteFailure(e: unknown): ProjectWriteFailure {
  const err = classifyAppError(e);
  return {
    kind: err.kind === "offline" ? "disconnected" : "unknown",
    message: err.message || undefined,
  };
}

export function DeleteProjectDialog({
  target,
  onClose,
  onDeleted,
}: {
  target: DeleteProjectTarget | null;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const [offlineMachines, setOfflineMachines] = React.useState<string[]>([]);
  const [memoryCount, setMemoryCount] = React.useState<number | undefined>();

  /*
    配了这个项目、但此刻联系不上的那几台。

    读不上来时留空名单 —— 包会因此说「都在线」。这句在读失败时可能不准，但把一次
    读失败翻成「有一批你看不见的机器」更糟：那是一句凭空捏造的坏消息。
  */
  const projectID = target?.id ?? 0;
  React.useEffect(() => {
    if (projectID <= 0) {
      setOfflineMachines([]);
      return;
    }
    let cancelled = false;
    void Promise.all([
      ProjectLocationList(projectID) as Promise<ProjectLocationView[]>,
      RemoteDeviceList() as Promise<DeviceView[]>,
    ])
      .then(([locs, devices]) => {
        if (cancelled) return;
        const configured = new Set((locs ?? []).map((l) => l.deviceId));
        setOfflineMachines(
          (devices ?? [])
            .filter((d) => configured.has(String(d.id)) && !d.online)
            .map((d) => d.name),
        );
      })
      .catch(() => {
        if (!cancelled) setOfflineMachines([]);
      });
    return () => {
      cancelled = true;
    };
  }, [projectID]);

  /*
    子树（含它自己）名下会一并删掉的记忆条数。

    读不上来时不传：包因此不提记忆，而不是说一句「没有记忆」去冒充读到的结果。
    计数口径沿用 MemoryCountsByOwner（只数正常条目）。
  */
  React.useEffect(() => {
    if (projectID <= 0) {
      setMemoryCount(undefined);
      return;
    }
    let cancelled = false;
    void Promise.all([
      ProjectListTree() as Promise<ProjectTreeView[]>,
      MemoryCountsByOwner(),
    ])
      .then(([tree, counts]) => {
        if (cancelled) return;
        const ids = subtreeIds(tree ?? [], projectID);
        setMemoryCount(
          (counts ?? [])
            .filter((c) => c.owner.kind === "project" && ids.has(c.owner.id))
            .reduce((sum, c) => sum + c.count, 0),
        );
      })
      .catch(() => {
        if (!cancelled) setMemoryCount(undefined);
      });
    return () => {
      cancelled = true;
    };
  }, [projectID]);

  const ports = React.useMemo<ProjectDeletePorts>(
    () => ({
      deleteProject: async (id) => {
        try {
          await ProjectDelete(Number(id));
          return { ok: true };
        } catch (e) {
          return { ok: false, failure: toDeleteFailure(e) };
        }
      },
    }),
    [],
  );

  if (!target) return null;

  return (
    <ProjectDeleteDialog
      open
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
      project={{ id: String(target.id), name: target.name }}
      childCount={target.childCount ?? 0}
      memoryCount={memoryCount}
      offlineMachines={offlineMachines}
      ports={ports}
      onDeleted={onDeleted}
    />
  );
}
