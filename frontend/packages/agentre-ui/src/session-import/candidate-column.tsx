/**
 * 导入对话框的左栏整体：筛选条 + 扫描状态 + 候选列表（规格「UI 与状态」）。
 *
 * 与 `candidate-list.tsx` 的分工：那份只管「有候选时怎么列」，这份管「这一栏
 * 此刻该显示八种状态里的哪一种」—— 扫描中 / 已停止 / 扫描失败 / 空 / 有结果，
 * 外加设备级与单后端两种故障条。**旧 daemon 不得被折成「这台机器没有会话」**
 * （规格「远端」），所以故障条与空态各占各的位置，谁都不吞掉谁。
 */
import { CircleAlert, Loader2, TriangleAlert } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { Button } from "../ui/button";
import { SearchInput } from "../ui/search-input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../ui/select";

import { CandidateList, useBackendLabel } from "./candidate-list";
import type {
  ImportCandidateView,
  ImportDeviceView,
  ImportScanIssue,
} from "./ports";
import { ALL_BACKENDS, type ScanState } from "./use-import-session";

/** 能导入的三个后端。builtin / openclaw 没有本地磁盘档案这回事（规格 Out of scope）。 */
const IMPORTABLE_BACKENDS = ["claudecode", "codex", "piagent"] as const;

export interface CandidateColumnProps {
  devices: readonly ImportDeviceView[];
  deviceId: string;
  onDeviceChange(deviceId: string): void;
  localDevice: ImportDeviceView | undefined;
  query: string;
  onQueryChange(query: string): void;
  backendFilter: string;
  onBackendFilterChange(value: string): void;
  scan: ScanState;
  candidates: readonly ImportCandidateView[];
  deviceIssue: ImportScanIssue | undefined;
  backendIssues: readonly ImportScanIssue[];
  activeLocator: string | null;
  onActivate(locator: string): void;
  now: number;
  cwdPrefix: string;
  scoped: boolean;
  onStopScan(): void;
  onRescan(): void;
  onRelaxFilters(): void;
  onOpenImported(sessionId: string): void;
}

export function CandidateColumn({
  devices,
  deviceId,
  onDeviceChange,
  localDevice,
  query,
  onQueryChange,
  backendFilter,
  onBackendFilterChange,
  scan,
  candidates,
  deviceIssue,
  backendIssues,
  activeLocator,
  onActivate,
  now,
  cwdPrefix,
  scoped,
  onStopScan,
  onRescan,
  onRelaxFilters,
  onOpenImported,
}: CandidateColumnProps) {
  const { t } = useUiTranslation();
  const backendLabel = useBackendLabel();

  return (
    <div className="flex w-[360px] shrink-0 flex-col border-r border-border">
      {/*
        搜索框先占满一行：360px 的栏里塞下「机器 + 搜索 + 后端」三个控件，
        搜索框只剩百来像素，一个项目路径都看不全。机器选择器只有多机时
        才出现，那时它下沉一行，搜索框的宽度不受它有没有影响。
      */}
      <div className="flex flex-col gap-2 border-b border-border px-3 py-2.5">
        <div className="flex items-center gap-2">
          <SearchInput
            value={query}
            onChange={onQueryChange}
            placeholder={t("importSession.searchPlaceholder")}
            aria-label={t("importSession.searchPlaceholder")}
            className="min-w-0 flex-1"
          />
          <Select value={backendFilter} onValueChange={onBackendFilterChange}>
            <SelectTrigger
              data-testid="import-backend-filter"
              aria-label={t("importSession.backendFilterAria")}
              className="h-8 w-[104px] shrink-0 text-xs"
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL_BACKENDS}>
                {t("importSession.allBackends")}
              </SelectItem>
              {IMPORTABLE_BACKENDS.map((b) => (
                <SelectItem key={b} value={b}>
                  {backendLabel(b)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        {devices.length > 1 ? (
          <Select value={deviceId} onValueChange={onDeviceChange}>
            <SelectTrigger
              data-testid="import-device-select"
              aria-label={t("importSession.deviceAria")}
              className="h-8 w-full text-xs"
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {devices.map((d) => (
                <SelectItem key={d.id} value={d.id}>
                  {d.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : null}
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-2 py-2">
        {/* 设备级：整台机器拨不通。不折成「这台机器没有会话」。 */}
        {deviceIssue ? (
          <div
            data-testid={`import-device-issue-${deviceIssue.status}`}
            role="alert"
            className="mb-2 flex flex-col gap-1.5 rounded-md border border-status-error/40 bg-destructive-soft px-3 py-2.5"
          >
            <span className="text-xs font-semibold text-status-error">
              {t("importSession.issue.unavailableTitle")}
            </span>
            <span className="text-2xs text-foreground">
              {deviceIssue.reason}
            </span>
            {localDevice && deviceId !== localDevice.id ? (
              <Button
                type="button"
                size="xs"
                variant="outline"
                className="self-start"
                data-testid="import-switch-local"
                onClick={() => onDeviceChange(localDevice.id)}
              >
                {t("importSession.issue.switchToLocal")}
              </Button>
            ) : null}
          </div>
        ) : null}

        {/* 单个后端读不动：其余照常出结果。 */}
        {backendIssues.map((issue) => (
          <div
            key={issue.backend}
            data-testid={`import-backend-issue-${issue.backend}`}
            role="alert"
            className="mb-2 flex flex-col gap-1 rounded-md border border-status-error/40 bg-destructive-soft px-3 py-2"
          >
            <span className="flex items-center gap-1.5 text-2xs font-semibold text-status-error">
              <TriangleAlert className="size-3" aria-hidden="true" />
              {t("importSession.issue.backendTitle", {
                backend: backendLabel(issue.backend),
              })}
            </span>
            <span className="text-2xs text-foreground">{issue.reason}</span>
            <span className="text-2xs text-muted-foreground">
              {t("importSession.issue.othersUnaffected")}
            </span>
          </div>
        ))}

        {scan.kind === "scanning" ? (
          <div
            data-testid="import-scanning"
            role="status"
            aria-live="polite"
            className="flex flex-col gap-2"
          >
            {/*
              「停止」贴着「正在扫描」那句话，而不是吊在骨架屏底下：它是
              这条状态的出口，读到状态的那一眼就该看见它；排在三块骨架屏
              之后，位置还随骨架屏数量浮动，等于让人往下找。
            */}
            <div
              data-testid="import-scan-status"
              className="flex items-center gap-2 px-1 py-1"
            >
              <Loader2
                className="size-3.5 shrink-0 animate-spin text-muted-foreground"
                aria-hidden="true"
              />
              <span className="min-w-0 flex-1 truncate text-2xs text-muted-foreground">
                {t("importSession.scan.running")}
              </span>
              <Button
                type="button"
                size="xs"
                variant="ghost"
                data-testid="import-scan-stop"
                onClick={onStopScan}
              >
                {t("importSession.scan.stop")}
              </Button>
            </div>
            {[0, 1, 2].map((i) => (
              <div
                key={i}
                aria-hidden="true"
                className="h-11 animate-pulse rounded-md bg-muted"
              />
            ))}
          </div>
        ) : null}

        {scan.kind === "stopped" ? (
          <div
            data-testid="import-scan-stopped"
            className="flex flex-col items-center gap-2 px-4 py-8 text-center"
          >
            <span className="text-xs text-muted-foreground">
              {t("importSession.scan.stopped")}
            </span>
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={onRescan}
            >
              {t("importSession.scan.rescan")}
            </Button>
          </div>
        ) : null}

        {scan.kind === "failed" ? (
          <div
            data-testid="import-scan-failed"
            role="alert"
            className="flex items-start gap-2 rounded-md border border-status-error/40 bg-destructive-soft px-3 py-2.5 text-status-error"
          >
            <CircleAlert
              className="mt-0.5 size-3.5 shrink-0"
              aria-hidden="true"
            />
            <span className="text-2xs break-words">{scan.message}</span>
          </div>
        ) : null}

        {scan.kind === "done" && candidates.length === 0 ? (
          <div
            data-testid="import-empty"
            className="flex flex-col items-center gap-2 px-4 py-10 text-center"
          >
            <span className="text-xs font-medium text-foreground">
              {t("importSession.empty.title")}
            </span>
            <span className="text-2xs text-muted-foreground">
              {cwdPrefix
                ? t("importSession.empty.bodyScoped", { path: cwdPrefix })
                : t("importSession.empty.body")}
            </span>
            {scoped ? (
              <Button
                type="button"
                size="sm"
                variant="outline"
                data-testid="import-relax-filters"
                onClick={onRelaxFilters}
              >
                {t("importSession.empty.relax")}
              </Button>
            ) : null}
          </div>
        ) : null}

        {scan.kind === "done" && candidates.length > 0 ? (
          <CandidateList
            candidates={candidates}
            activeLocator={activeLocator}
            onActivate={(c) => onActivate(c.locator)}
            onOpenImported={onOpenImported}
            now={now}
          />
        ) : null}
      </div>
    </div>
  );
}
