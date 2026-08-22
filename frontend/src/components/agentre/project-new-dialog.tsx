/**
 * 桌面端这一侧的「新建项目」—— **只剩 adapter**（规格 2026-08-22 B 段，决策 9）。
 *
 * 表单本身住在 `@agentre-ai/agentre-ui` 里，两端同一份。这里做的只有：把项目树拍平
 * 成父项目候选，把 wails 那三个绑定（`ProjectCreate` / `SelectDirectory` /
 * `ProjectDetectGitRepo`）翻成 `ProjectCreatePorts`。
 *
 * **本机路径在这一端仍是必填**，用 `localPathRequired` 如实声明出来。规格决策 9 要的
 * 是两端都不必填，但这一端的后端今天建不出没有路径的项目：`ProjectCreateRequest`
 * （internal/app/project.go）没有 `LocalPathMissing` 这一格，而 `Project.Check` 在它
 * 为 false 时要求 `Path` 非空。让按钮亮着然后必然被后端拒，比当场说「这一格得填」
 * 更糟。补齐要动 Go —— 本轮的硬不变量禁止，所以先把这件事说出口。
 */
import * as React from "react";
import { useTranslation } from "react-i18next";

import {
  ProjectCreateDialog,
  type ProjectCreatePorts,
} from "@agentre-ai/agentre-ui";

import { IconPicker } from "./icon-picker";
import {
  ProjectCreate,
  ProjectDetectGitRepo,
  SelectDirectory,
} from "../../../wailsjs/go/app/App";
import type { app } from "../../../wailsjs/go/models";
import type { AgentColor } from "./types";

type ProjectTreeNode = app.ProjectTreeNode;

export type ProjectNewDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  tree: ProjectTreeNode[];
  /** 用户点 + 时如果当前选中某个项目，可用作默认父项目 ID。 */
  initialParentID?: number;
  /** 创建成功时回调；调用方触发 refresh + 选中新项目。 */
  onCreated: (projectID: number) => void;
};

/** 项目树拍平成 [{id, name, depth}] 供父项目下拉用，depth 决定缩进。 */
function flattenTree(
  nodes: ProjectTreeNode[],
  depth = 0,
): { id: string; name: string; depth: number }[] {
  const out: { id: string; name: string; depth: number }[] = [];
  for (const n of nodes) {
    if (!n.project) continue;
    out.push({ id: String(n.project.id), name: n.project.name, depth });
    if (n.children) out.push(...flattenTree(n.children, depth + 1));
  }
  return out;
}

function ProjectNewDialog({
  open,
  onOpenChange,
  tree,
  initialParentID = 0,
  onCreated,
}: ProjectNewDialogProps) {
  const { t } = useTranslation();
  const parentOptions = React.useMemo(() => flattenTree(tree), [tree]);

  const ports = React.useMemo<ProjectCreatePorts>(
    () => ({
      create: async (draft) => {
        try {
          const created = await ProjectCreate({
            parentID: Number(draft.parentId ?? 0),
            name: draft.name,
            icon: draft.icon ?? "folder",
            color: draft.color ?? "agent-1",
            description: draft.description ?? "",
            path: draft.localPath ?? "",
            initialAgentIDs: [],
          });
          return { ok: true, id: String(created.id) };
        } catch (e) {
          // 跨 wails 只剩一句本地化文本、没有码可读，所以分不出类就说分不出。
          return {
            ok: false,
            failure: { kind: "unknown", message: String(e) },
          };
        }
      },
      pickLocalDirectory: async () => {
        try {
          return (
            (await SelectDirectory(t("projectNew.selectDirectory"))) || null
          );
        } catch {
          // 用户取消 —— 不是失败。
          return null;
        }
      },
      probeGitRepo: async (path) => {
        try {
          const info = await ProjectDetectGitRepo(path);
          return {
            isGitRepo: !!info?.isGitRepo,
            branch: info?.currentBranch,
            origin: info?.origin,
          };
        } catch {
          // 探不出来就什么都不标 —— 编一个「不是仓库」比不说更糟。
          return null;
        }
      },
      localPathRequired: true,
    }),
    [t],
  );

  return (
    <ProjectCreateDialog
      open={open}
      onOpenChange={onOpenChange}
      parentOptions={parentOptions}
      initialParentId={initialParentID > 0 ? String(initialParentID) : ""}
      ports={ports}
      iconField={({ value, color, onPick }) => (
        <label className="flex flex-col gap-1.5 text-xs">
          <span className="font-medium text-foreground">
            {t("org.department.icon")}
          </span>
          <IconPicker
            value={value || "folder"}
            onChange={onPick}
            // 包递的是颜色 token 字符串；这一端的 IconPicker 收的是它的窄类型。
            accentColor={(color || "agent-1") as AgentColor}
          />
        </label>
      )}
      onCreated={(id) => onCreated(Number(id))}
    />
  );
}

export { ProjectNewDialog };
