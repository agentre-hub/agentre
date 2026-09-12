/**
 * 删除项目的确认，两端共用那一份（规格 2026-08-22 B 段，决策 8）。
 *
 * 危险确认是**一种形态**而不是一段文案：头部 danger、主按钮 destructive，
 * **后果写在正文，不写进标题**。
 *
 * 按下去之前要说清四件事，因为它们各不相同、又都容易被想错：
 *
 *   1. 子项目一并删；
 *   2. **对话一条都不删** —— 项目归属是判出来的，项目行没了它们回「随手对话」；
 *   3. 机器上的代码目录一个字节都不动，删掉的只是「这台机器上这个项目在哪」；
 *   4. 当前离线的机器要等下次上线才跟着删。
 *
 * **要求输入完整项目名才放行**：两端向更谨慎的那一端对齐。删除连子项目一起删，
 * 是一次不可逆的批量后果，而组头 ⋮ 上「删除」与「设置」只隔两项，误点代价不对称。
 */
import * as React from "react";

import { useUiTranslation } from "../i18n";
import { Button } from "../ui/button";
import {
  DialogShell,
  DialogShellBody,
  DialogShellFooter,
  DialogShellHeader,
  DialogShellSubmit,
} from "../ui/dialog-shell";
import { Input } from "../ui/input";
import type { ProjectDeletePorts } from "./ports";

export interface ProjectDeleteDialogProps {
  open: boolean;
  onOpenChange(open: boolean): void;
  project: { id: string; name: string };
  /** 这棵子树下还有几个项目（不含它自己）。 */
  childCount: number;
  /**
   * 配了这个项目、但此刻联系不上的机器名。空数组 = 都在线。
   *
   * 由宿主点名而不是包去猜：这两句话说的是**不同的事**，给一个空名单配上「这些
   * 机器离线」的句式，等于告诉用户有一批他看不见的机器。
   */
  offlineMachines: string[];
  ports: ProjectDeletePorts;
  onDeleted(projectId: string): void;
}

export function ProjectDeleteDialog({
  open,
  onOpenChange,
  project,
  childCount,
  offlineMachines,
  ports,
  onDeleted,
}: ProjectDeleteDialogProps) {
  const { t } = useUiTranslation();
  const [confirm, setConfirm] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  function handleOpenChange(next: boolean) {
    if (!next) {
      setConfirm("");
      setBusy(false);
      setError(null);
    }
    onOpenChange(next);
  }

  // 首尾空白不算错：复制粘贴项目名常带一个尾空格，为此拦下来只是刁难。
  const canDelete = confirm.trim() === project.name && !busy;

  function submit() {
    if (!canDelete) return;
    setBusy(true);
    setError(null);
    void ports.deleteProject(project.id).then(
      (outcome) => {
        setBusy(false);
        if (!outcome.ok) {
          setError(
            outcome.failure.message?.trim() ||
              t("projectSettings.delete.failed"),
          );
          return;
        }
        onDeleted(project.id);
        handleOpenChange(false);
      },
      (e: unknown) => {
        setBusy(false);
        setError(String(e));
      },
    );
  }

  return (
    <DialogShell
      open={open}
      onOpenChange={handleOpenChange}
      size="sm"
      danger
      busy={busy}
    >
      {/* 标题只有一句话；后果全在正文里。 */}
      <DialogShellHeader
        title={t("projectSettings.delete.title", { name: project.name })}
        danger
        onClose={() => handleOpenChange(false)}
        busy={busy}
      />
      <DialogShellBody>
        <ul className="space-y-2 text-xs text-muted-foreground">
          <li data-testid="delete-project-children">
            {childCount > 0
              ? t("projectSettings.delete.children", { count: childCount })
              : t("projectSettings.delete.noChildren")}
          </li>
          <li data-testid="delete-project-sessions">
            {t("projectSettings.delete.sessions")}
          </li>
          <li data-testid="delete-project-files">
            {t("projectSettings.delete.files")}
          </li>
          <li data-testid="delete-project-offline">
            {offlineMachines.length > 0
              ? t("projectSettings.delete.offline", {
                  names: offlineMachines.join("、"),
                })
              : t("projectSettings.delete.noOffline")}
          </li>
        </ul>
        <label className="mt-4 block text-xs font-medium text-foreground">
          {t("projectSettings.delete.confirmName", { name: project.name })}
          <Input
            data-testid="delete-project-confirm"
            autoFocus
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            placeholder={project.name}
            className="mt-1 font-mono text-xs"
          />
        </label>
      </DialogShellBody>
      <div data-testid="delete-project-footer">
        <DialogShellFooter error={error}>
          <Button variant="ghost" onClick={() => handleOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <DialogShellSubmit
            data-testid="delete-project-submit"
            variant="destructive"
            busy={busy}
            disabled={!canDelete}
            onClick={submit}
          >
            {t("projectSettings.delete.submit")}
          </DialogShellSubmit>
        </DialogShellFooter>
      </div>
    </DialogShell>
  );
}
