/**
 * 导入本地会话的对话框（规格 2026-08-26，决策 12 / 14 / 15 / 16）。
 *
 * **880px 双栏**（左候选 360px / 右预览）：同名会话在单列列表里只差轮数与时间，
 * 认不出该导哪条 —— 量测过 360px 时中英文标题都不截断，300px 时英文截断。
 *
 * **一次只导一条**（决策 12）：续跑要求每条各自绑 agent 并确认 cwd，批量勾选的
 * 多条往往来自不同 cwd、不同后端，共用不了一份确认。
 *
 * **续跑三要素收在按钮旁边**（决策 15）：agent 选择器（带出 backend / model）＋
 * cwd 存在性。cwd 没了就降级为只读导入（决策 16），并把后果写在按钮上
 * （「仅导入转录」而不是「导入并可继续对话」）。
 *
 * 状态齐全是这一件的验收点：扫描中 / 空 / 单后端失败 / 远端不支持 / 预览失败 /
 * 导入中 / 已导入 / cwd 不存在，八种各有各的话。**旧 daemon 不得被显示成「这台
 * 机器没有会话」**（规格「远端」）。
 *
 * 这份文件只做装配：状态与三条异步线在 `use-import-session.ts`，左栏在
 * `candidate-column.tsx`，底栏在 `import-footer.tsx`，右栏在 `preview-pane.tsx`。
 */
import { useUiTranslation } from "../i18n";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";

import { CandidateColumn } from "./candidate-column";
import { ImportFooter } from "./import-footer";
import { PreviewPane } from "./preview-pane";
import { useImportSession } from "./use-import-session";
import type { ImportDialogPrefill } from "./use-import-session";
import type { ImportOutcome, SessionImportPorts } from "./ports";

/** 预填的定义与状态住在一起；这里保持它自本模块出得去（barrel 钉着这条）。 */
export type { ImportDialogPrefill };

export interface ImportSessionDialogProps {
  open: boolean;
  onOpenChange(open: boolean): void;
  ports: SessionImportPorts;
  prefill?: ImportDialogPrefill;
  /** 导入完成（含「早就导过」那一支）后宿主要做的事：刷新索引、跳过去。 */
  onImported(outcome: ImportOutcome): void;
}

export function ImportSessionDialog({
  open,
  onOpenChange,
  ports,
  prefill,
  onImported,
}: ImportSessionDialogProps) {
  const { t } = useUiTranslation();
  const state = useImportSession({
    open,
    ports,
    prefill,
    onImported,
    onOpenChange,
  });

  const openImported = (id: string) => {
    ports.openSession(id);
    onOpenChange(false);
  };

  const description = state.device?.local
    ? state.cwdPrefix
      ? t("importSession.descriptionLocalScoped", { path: state.cwdPrefix })
      : t("importSession.descriptionLocal")
    : state.cwdPrefix
      ? t("importSession.descriptionDeviceScoped", {
          device: state.device?.name ?? "",
          path: state.cwdPrefix,
        })
      : t("importSession.descriptionDevice", {
          device: state.device?.name ?? "",
        });

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        // 导入正在写库，关掉只会让人以为没提交。
        if (!next && state.importing) return;
        onOpenChange(next);
      }}
    >
      <DialogContent
        data-testid="import-session-dialog"
        /*
          高度先于内容定死：转录长度是外面的世界给的（几十轮到上千轮都有），
          让它决定对话框多高，等于让磁盘上那条会话把窗口顶穿屏幕。定高之后
          三行网格里只有中间那行是 `1fr`，头/底恒在视野内，滚动落到两栏各自
          的容器里；`max-h` 让小屏上先缩的是中间那行而不是溢出。
        */
        className="h-[680px] max-h-[calc(100vh-6rem)] max-w-[880px] grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden"
      >
        <DialogHeader>
          <DialogTitle>
            {prefill?.scopeLabel
              ? t("importSession.titleScoped", { scope: prefill.scopeLabel })
              : t("importSession.title")}
          </DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>

        <div className="flex min-h-0">
          {/* ── 左栏：候选（360px，量测过的那一档） ─────────────────── */}
          <CandidateColumn
            devices={ports.devices}
            deviceId={state.deviceId}
            onDeviceChange={state.setDeviceId}
            localDevice={state.localDevice}
            query={state.query}
            onQueryChange={state.setQuery}
            backendFilter={state.backendFilter}
            onBackendFilterChange={state.setBackendFilter}
            scan={state.scan}
            candidates={state.candidates}
            deviceIssue={state.deviceIssue}
            backendIssues={state.backendIssues}
            activeLocator={state.activeLocator}
            onActivate={state.setActiveLocator}
            now={state.now}
            cwdPrefix={state.cwdPrefix}
            scoped={state.scoped}
            onStopScan={state.stopScan}
            onRescan={state.rescan}
            onRelaxFilters={state.relaxFilters}
            onOpenImported={openImported}
          />

          {/* ── 右栏：真实转录预览 ───────────────────────────────────── */}
          <PreviewPane state={state.preview} onOpenImported={openImported} />
        </div>

        {/* ── 底部：续跑三要素 + 主按钮（决策 15） ───────────────────── */}
        <ImportFooter
          active={state.active}
          agent={state.agent}
          agentOptions={state.agentOptions}
          onAgentChange={state.setAgentId}
          previewReady={state.previewReady}
          cwdExists={state.cwdExists}
          cwdOverride={state.cwdOverride}
          importing={state.importing}
          importError={state.importError}
          canImport={state.canImport}
          onSubmit={state.runImport}
          onDismiss={() => onOpenChange(false)}
          onCancelImport={
            ports.cancelImport ? state.cancelRunningImport : undefined
          }
          onPickDirectory={
            ports.pickDirectory ? state.pickDirectory : undefined
          }
        />
      </DialogContent>
    </Dialog>
  );
}
