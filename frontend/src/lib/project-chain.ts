// frontend/src/lib/project-chain.ts
import type { app } from "../../wailsjs/go/models";

type ProjectTreeNode = app.ProjectTreeNode;

// projectChain 从树里走一条 root → 目标节点的 name 链, 供 breadcrumb / tooltip 显示。
// 找不到返回 []。
export function projectChain(
  nodes: ProjectTreeNode[],
  targetId: number,
  trail: string[] = [],
): string[] {
  for (const n of nodes) {
    const id = n.project?.id ?? 0;
    if (id <= 0) continue;
    const next = [...trail, n.project?.name ?? ""];
    if (id === targetId) return next;
    if (n.children && n.children.length > 0) {
      const found = projectChain(n.children, targetId, next);
      if (found.length > 0) return found;
    }
  }
  return [];
}

// findProject 在树里找目标 project;找不到返回 null。颜色/路径两个取值口共用这一趟遍历。
function findProject(
  nodes: ProjectTreeNode[],
  targetId: number,
): app.ProjectItem | null {
  for (const n of nodes) {
    if ((n.project?.id ?? 0) === targetId) return n.project ?? null;
    if (n.children?.length) {
      const found = findProject(n.children, targetId);
      if (found) return found;
    }
  }
  return null;
}

// findProjectColorToken 找目标 project 的 color token (例如 "agent-1")。
// 找不到 / 空 token 返回 null。
export function findProjectColorToken(
  nodes: ProjectTreeNode[],
  targetId: number,
): string | null {
  return findProject(nodes, targetId)?.color || null;
}

// findProjectPath 返回目标项目配置的本地工作目录。找不到或未配置时返回空串，
// 让调用方仅发现全局 Skill。
export function findProjectPath(
  nodes: ProjectTreeNode[],
  targetId: number,
): string {
  return findProject(nodes, targetId)?.path ?? "";
}
