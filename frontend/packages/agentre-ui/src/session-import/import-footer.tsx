/**
 * 导入对话框的底栏：续跑三要素 + 主按钮（决策 15 / 16）。
 *
 * 三要素（agent 选择器、后端与模型胶囊、cwd 存在性）收在按钮旁边，而不是散在
 * 右栏的元信息里：它们是「按下去会发生什么」的前提，读的人的视线此刻已经在
 * 按钮上。cwd 没了就降级为只读导入，并把后果写在按钮上（「仅导入转录」）。
 */
import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { AgentBackendLogo } from "../engine/ai-brand-logo";
import { Button } from "../ui/button";
import { DialogFooter } from "../ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../ui/select";

import { useBackendLabel } from "./candidate-list";
import type {
  ImportAgentOption,
  ImportCandidateView,
  ImportPreviewResult,
} from "./ports";
import type { ImportProgress } from "./use-import-session";

export interface ImportFooterProps {
  active: ImportCandidateView | null;
  agent: ImportAgentOption | null;
  agentOptions: readonly ImportAgentOption[];
  onAgentChange(agentId: string): void;
  previewReady: ImportPreviewResult | null;
  cwdExists: boolean;
  cwdOverride: string;
  importing: ImportProgress | null;
  importError: string;
  canImport: boolean;
  onSubmit(): void;
  onDismiss(): void;
  /** 宿主接不住取消就不给：不承诺做不到的事（见下方注释）。 */
  onCancelImport?: () => void;
  /** 宿主弹不出目录选择器就别摆那颗键。 */
  onPickDirectory?: () => void;
}

export function ImportFooter({
  active,
  agent,
  agentOptions,
  onAgentChange,
  previewReady,
  cwdExists,
  cwdOverride,
  importing,
  importError,
  canImport,
  onSubmit,
  onDismiss,
  onCancelImport,
  onPickDirectory,
}: ImportFooterProps) {
  const { t } = useUiTranslation();
  const backendLabel = useBackendLabel();

  return (
    <DialogFooter className="flex-col items-stretch justify-start gap-2">
      {importing ? (
        <div
          data-testid="import-progress"
          role="status"
          aria-live="polite"
          className="flex flex-col gap-1"
        >
          <span className="text-xs text-foreground">
            {t("importSession.importing.title", {
              title: active?.title ?? "",
            })}
          </span>
          <div
            className="h-1.5 w-full overflow-hidden rounded-full bg-muted"
            role="progressbar"
            aria-valuemin={0}
            aria-valuemax={importing.total || undefined}
            aria-valuenow={importing.done}
          >
            <div
              className="h-full bg-primary transition-[width]"
              style={{
                width:
                  importing.total > 0
                    ? `${Math.round((importing.done / importing.total) * 100)}%`
                    : "10%",
              }}
            />
          </div>
          <span className="text-2xs text-muted-foreground">
            {importing.total > 0
              ? t("importSession.importing.progress", {
                  done: importing.done,
                  total: importing.total,
                })
              : t("importSession.importing.progressUnknown", {
                  done: importing.done,
                })}
          </span>
        </div>
      ) : null}

      <div
        data-testid="import-actions"
        className="flex flex-wrap items-center gap-2"
      >
        <span className="text-2xs text-muted-foreground">
          {t("importSession.adopt.label")}
        </span>
        <Select
          value={agent?.id ?? ""}
          onValueChange={onAgentChange}
          disabled={!active || active.imported || agentOptions.length === 0}
        >
          <SelectTrigger
            data-testid="import-agent-select"
            aria-label={t("importSession.adopt.agentAria")}
            className="h-8 w-[176px] text-xs"
          >
            <SelectValue placeholder={t("importSession.adopt.pickAgent")} />
          </SelectTrigger>
          <SelectContent>
            {agentOptions.map((a) => (
              <SelectItem key={a.id} value={a.id}>
                {a.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {active ? (
          <span
            data-testid="import-backend-pill"
            className="inline-flex items-center gap-1 rounded-md border border-border px-1.5 py-0.5 text-2xs text-muted-foreground"
          >
            <AgentBackendLogo
              backendType={active.backend}
              className="size-3.5"
            />
            {backendLabel(active.backend)}
          </span>
        ) : null}
        {previewReady?.meta.model ? (
          <span className="rounded-md border border-border px-1.5 py-0.5 font-mono text-2xs text-muted-foreground">
            {previewReady.meta.model}
          </span>
        ) : null}
        <span className="min-w-0 flex-1" />
        {/*
          导入一开始，主操作位就整个换成「停止导入」——而不是把它按灰、另在
          进度条旁边补一颗小小的取消键。同屏两颗都写着「取消/停止」的键，
          一颗还按不动，读的人得先分辨哪颗才是能停下这笔写入的那颗；此刻
          唯一还成立的动作就是「停」，它就该占着眼睛已经在的那个位置。

          取消是可选 port：宿主接不住就维持原来那两颗（禁用态），不承诺
          做不到的事。写入侧整笔回滚，所以按下去库里不留半截会话。
        */}
        {importing && onCancelImport ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            data-testid="import-cancel"
            onClick={onCancelImport}
          >
            {t("importSession.importing.cancel")}
          </Button>
        ) : (
          <>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={!!importing}
              onClick={onDismiss}
            >
              {t("common.cancel")}
            </Button>
            <Button
              type="button"
              size="sm"
              data-testid="import-submit"
              disabled={!canImport}
              onClick={onSubmit}
            >
              {previewReady && !cwdExists
                ? t("importSession.adopt.submitReadOnly")
                : t("importSession.adopt.submit")}
            </Button>
          </>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-2">
        {previewReady ? (
          <span
            data-testid="import-cwd-line"
            className={cn(
              "text-2xs",
              cwdExists || cwdOverride
                ? "text-muted-foreground"
                : "text-status-error",
            )}
          >
            {cwdOverride
              ? t("importSession.adopt.cwdAdopted", { path: cwdOverride })
              : cwdExists
                ? t("importSession.adopt.cwdOk", {
                    path: previewReady.meta.cwd,
                  })
                : t("importSession.adopt.cwdMissing", {
                    path: previewReady.meta.cwd,
                  })}
          </span>
        ) : null}
        {/*
          「选择新目录」是规格「续跑」在 cwd 没了那一支给的出口：选中的目录成为
          **这条会话的工作目录**（随 runImport 交回宿主，落在会话那一列上），
          不是改扫描筛选。它不会让这条会话变得可续跑（决策 16），换来的是
          「接着聊时从哪儿起 CLI」有了答案。

          可选 port：宿主弹不出目录选择器就别摆这颗键。
        */}
        {previewReady && !cwdExists && onPickDirectory ? (
          <Button
            type="button"
            size="xs"
            variant="outline"
            data-testid="import-pick-directory"
            onClick={onPickDirectory}
          >
            {t("importSession.adopt.pickDirectory")}
          </Button>
        ) : null}
        {importError ? (
          <span
            data-testid="import-error"
            role="alert"
            className="text-2xs text-status-error"
          >
            {importError}
          </span>
        ) : null}
      </div>
    </DialogFooter>
  );
}
