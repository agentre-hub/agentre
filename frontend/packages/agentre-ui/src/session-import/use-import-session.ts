/**
 * 导入对话框的全部状态与副作用（规格 2026-08-26）。
 *
 * 抽成一支 hook 是为了让 `import-dialog.tsx` 只剩「装配」：扫描/预览/续跑三条
 * 异步线各自带着自己的作废规矩（token、nonce、取消标记），混在 JSX 中间读不出
 * 哪一行在防哪一种竞态。**hook 的调用顺序就是原来的顺序**，一条都不许挪动。
 */
import * as React from "react";

import type { PreviewState } from "./preview-pane";
import type {
  ImportAgentOption,
  ImportCandidateView,
  ImportCandidatesResult,
  ImportDeviceView,
  ImportOutcome,
  ImportPreviewResult,
  ImportScanIssue,
  SessionImportPorts,
} from "./ports";

/** 后端筛选器里的「全部」。Radix 的 Item 不收空串值。 */
export const ALL_BACKENDS = "__all__";

/**
 * 这一笔导入的标识。宿主拿它把写到一半的导入停掉（写入侧整笔回滚）。
 *
 * 自己拼而不是用 `crypto.randomUUID()`：这份包也跑在没有那支 API 的宿主里，
 * 而这个值只要在「同时在跑的几笔之间」唯一就够了。
 */
function newRequestId(): string {
  return `imp-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

/** 稳定的空列表：扫描没回来时别每次 render 新建一个数组。 */
const EMPTY_CANDIDATES: readonly ImportCandidateView[] = [];
const EMPTY_ISSUES: readonly ImportScanIssue[] = [];

/** 入口按自己那一维预填（决策 13）。四条轴各填各的，缺省就是不填。 */
export interface ImportDialogPrefill {
  /** 标题后半段：项目名 / 机器名 / Agent 名。空串就只有「导入本地会话」。 */
  scopeLabel?: string;
  /** 机器组头进入即锁定那台机器；其余入口默认本机。 */
  deviceId?: string;
  /** 项目组头进入预填该项目的路径。 */
  cwdPrefix?: string;
  /** Agent 组头进入预选那个 agent。 */
  agentId?: string;
  /** 项目组头进入时新会话归到这个项目。 */
  projectId?: string;
}

export type ScanState =
  | { kind: "scanning" }
  | { kind: "done"; result: ImportCandidatesResult }
  /** 用户按了「停止」：不再等这一次的结果（后台那次扫描的结果直接丢弃）。 */
  | { kind: "stopped" }
  | { kind: "failed"; message: string };

/** 导入进行中的进度。`requestId` 是宿主用来叫停这一笔的凭据。 */
export interface ImportProgress {
  done: number;
  total: number;
  requestId: string;
}

function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

export interface UseImportSessionArgs {
  open: boolean;
  ports: SessionImportPorts;
  prefill?: ImportDialogPrefill;
  onImported(outcome: ImportOutcome): void;
  onOpenChange(open: boolean): void;
}

export interface ImportSessionState {
  deviceId: string;
  setDeviceId(id: string): void;
  localDevice: ImportDeviceView | undefined;
  device: ImportDeviceView | null;
  cwdPrefix: string;
  query: string;
  setQuery(query: string): void;
  backendFilter: string;
  setBackendFilter(value: string): void;
  scoped: boolean;
  scan: ScanState;
  candidates: readonly ImportCandidateView[];
  deviceIssue: ImportScanIssue | undefined;
  backendIssues: readonly ImportScanIssue[];
  activeLocator: string | null;
  setActiveLocator(locator: string): void;
  active: ImportCandidateView | null;
  now: number;
  preview: PreviewState;
  previewReady: ImportPreviewResult | null;
  cwdExists: boolean;
  cwdOverride: string;
  agentOptions: readonly ImportAgentOption[];
  agent: ImportAgentOption | null;
  setAgentId(id: string): void;
  importing: ImportProgress | null;
  importError: string;
  canImport: boolean;
  runImport(): void;
  stopScan(): void;
  rescan(): void;
  relaxFilters(): void;
  cancelRunningImport(): void;
  pickDirectory(): void;
}

export function useImportSession({
  open,
  ports,
  prefill,
  onImported,
  onOpenChange,
}: UseImportSessionArgs): ImportSessionState {
  const localDevice = ports.devices.find((d) => d.local) ?? ports.devices[0];
  const [deviceId, setDeviceId] = React.useState(
    prefill?.deviceId ?? localDevice?.id ?? "",
  );
  const [cwdPrefix, setCwdPrefix] = React.useState(prefill?.cwdPrefix ?? "");
  const [query, setQuery] = React.useState("");
  const [backendFilter, setBackendFilter] = React.useState(ALL_BACKENDS);
  const [scan, setScan] = React.useState<ScanState>({ kind: "scanning" });
  const [activeLocator, setActiveLocator] = React.useState<string | null>(null);
  const [preview, setPreview] = React.useState<PreviewState>({ kind: "idle" });
  const [agentId, setAgentId] = React.useState(prefill?.agentId ?? "");
  const [importing, setImporting] = React.useState<ImportProgress | null>(null);
  /** 用户自己按了取消：随后那次 reject 是他要的结果，不该再刷一条红字。 */
  const canceledRef = React.useRef(false);
  /**
   * 「选择新目录」选中的那个目录 —— 这条会话去哪跑，不是候选列表的筛选条件
   * （规格「续跑」的那条出口）。换一条候选就作废：它是跟着某一条会话选的。
   */
  const [cwdOverride, setCwdOverride] = React.useState("");
  const [importError, setImportError] = React.useState("");
  /** 「重新扫描」靠它推一把：筛选条件没变，只靠 deps 是不会重跑的。 */
  const [scanNonce, setScanNonce] = React.useState(0);
  /** 每次「现在」只取一次：分组与时刻格式化共用，免得每行各 new 一个 Date。 */
  const [now] = React.useState(() => Date.now());

  const backends = React.useMemo(
    () => (backendFilter === ALL_BACKENDS ? [] : [backendFilter]),
    [backendFilter],
  );

  /**
   * 扫描。每次请求带一个 token，只有最后一次的结果落地 —— 换设备/改筛选时先到的
   * 旧结果会盖掉新结果，那正是「切到本机还看着构建机的会话」的由来。
   */
  const scanToken = React.useRef(0);
  React.useEffect(() => {
    if (!open) return;
    const token = ++scanToken.current;
    setScan({ kind: "scanning" });
    setActiveLocator(null);
    setPreview({ kind: "idle" });
    setCwdOverride("");
    const timer = setTimeout(
      () => {
        ports
          .listCandidates({ deviceId, backends, cwdPrefix, titleQuery: query })
          .then(
            (result) => {
              if (scanToken.current !== token) return;
              setScan({ kind: "done", result });
            },
            (err: unknown) => {
              if (scanToken.current !== token) return;
              setScan({ kind: "failed", message: errorMessage(err) });
            },
          );
        // 输入中不要每个字符打一次磁盘遍历；首次打开（query 为空）立刻扫。
      },
      query === "" ? 0 : 200,
    );
    return () => clearTimeout(timer);
    // ports 由宿主在 render 间保持稳定；把它放进依赖会让每次 render 重扫。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, deviceId, cwdPrefix, backends, query, scanNonce]);

  // 空数组也要走 memo：行内 `[]` 每次 render 都是新身份，会让下面的
  // `active`（进而是预览 effect）每次 render 都重算，预览就永远在重新拉。
  const candidates = React.useMemo<readonly ImportCandidateView[]>(
    () => (scan.kind === "done" ? scan.result.candidates : EMPTY_CANDIDATES),
    [scan],
  );
  const issues: readonly ImportScanIssue[] =
    scan.kind === "done" ? scan.result.issues : EMPTY_ISSUES;
  const deviceIssue = issues.find((i) => i.backend === "");
  const backendIssues = issues.filter((i) => i.backend !== "");

  const active = React.useMemo(
    () => candidates.find((c) => c.locator === activeLocator) ?? null,
    [candidates, activeLocator],
  );

  // 换一条候选就丢掉上一条选好的目录：它是为**那条**会话选的，跟着走只会让
  // 下一条会话悄悄落在一个用户没为它选过的目录里。
  React.useEffect(() => setCwdOverride(""), [activeLocator]);

  /** 预览。与扫描同一套 token 规矩。 */
  const previewToken = React.useRef(0);
  React.useEffect(() => {
    const token = ++previewToken.current;
    if (!active) {
      setPreview({ kind: "idle" });
      return;
    }
    if (active.imported) {
      setPreview({ kind: "imported", sessionId: active.importedSessionId });
      return;
    }
    setPreview({ kind: "loading" });
    ports
      .preview({
        deviceId,
        backend: active.backend,
        locator: active.locator,
      })
      .then(
        (result) => {
          if (previewToken.current !== token) return;
          setPreview({ kind: "ready", result });
        },
        (err: unknown) => {
          if (previewToken.current !== token) return;
          setPreview({ kind: "error", message: errorMessage(err) });
        },
      );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active, deviceId]);

  /**
   * 进度经事件到达而不是返回值（长任务的既定路子）。
   *
   * 订阅在**对话框打开时**就挂上，而不是等按下按钮才挂：写入是同步发起的，
   * 头几轮的事件会赶在订阅之前到，那正是进度条从 40% 开始跳的由来。
   * 没在导入时到的事件直接丢掉（上一次导入的尾巴不该改这一次的数字）。
   */
  React.useEffect(() => {
    if (!open || !ports.onImportProgress) return;
    return ports.onImportProgress((done, total) =>
      setImporting((current) =>
        current ? { ...current, done, total } : current,
      ),
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  /**
   * 只能选与该候选后端相同的 agent（规格「续跑」）：选一个 codex agent 去接一条
   * claude 会话，CLI 那边根本不认识这个 id。
   */
  const agentOptions = React.useMemo(() => {
    if (!active) return [];
    return ports.agents.filter((a) => a.backend === active.backend);
  }, [active, ports.agents]);
  const agent = agentOptions.find((a) => a.id === agentId) ?? null;

  // 预填的 agent 与这条候选的后端对不上时（Agent 组头进来却选了别的后端的会话），
  // 让它落空而不是硬留着 —— 留着会让按钮以为三要素齐了。
  //
  // **只在选中了候选之后判**：还没选时 `agentOptions` 恒为空，无条件清一遍等于
  // 把入口的预填当场抹掉，四条轴里 Agent 那一条的预填就永远不生效。
  React.useEffect(() => {
    if (!active) return;
    if (agentId && !agentOptions.some((a) => a.id === agentId)) setAgentId("");
  }, [active, agentOptions, agentId]);

  const previewReady = preview.kind === "ready" ? preview.result : null;
  const cwdExists = previewReady?.meta.cwdExists ?? false;
  const canImport =
    !!active && !active.imported && !!previewReady && !!agent && !importing;

  const runImport = React.useCallback(() => {
    if (!active || !agent) return;
    setImportError("");
    canceledRef.current = false;
    const requestId = newRequestId();
    setImporting({ done: 0, total: previewReady?.meta.turns ?? 0, requestId });
    ports
      .runImport({
        deviceId,
        backend: active.backend,
        locator: active.locator,
        agentId: agent.id,
        projectId: prefill?.projectId ?? "",
        cwd: cwdOverride,
        requestId,
      })
      .then(
        (outcome) => {
          setImporting(null);
          onImported(outcome);
          onOpenChange(false);
        },
        (err: unknown) => {
          setImporting(null);
          // 取消之后那次失败是用户自己要的，报出来只会让人以为出了别的问题。
          if (canceledRef.current) return;
          setImportError(errorMessage(err));
        },
      );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active, agent, deviceId, previewReady, prefill?.projectId, cwdOverride]);

  const device = ports.devices.find((d) => d.id === deviceId) ?? null;
  const scoped = cwdPrefix !== "" || query !== "" || backends.length > 0;

  return {
    deviceId,
    setDeviceId,
    localDevice,
    device,
    cwdPrefix,
    query,
    setQuery,
    backendFilter,
    setBackendFilter,
    scoped,
    scan,
    candidates,
    deviceIssue,
    backendIssues,
    activeLocator,
    setActiveLocator,
    active,
    now,
    preview,
    previewReady,
    cwdExists,
    cwdOverride,
    agentOptions,
    agent,
    setAgentId,
    importing,
    importError,
    canImport,
    runImport,
    // 只是不再等这一次的结果：磁盘遍历是那一侧的事，这里能给的
    // 保证只有「界面不再被它挂着」，所以文案说的是「停止」。
    stopScan: () => {
      scanToken.current += 1;
      setScan({ kind: "stopped" });
    },
    rescan: () => setScanNonce((n) => n + 1),
    relaxFilters: () => {
      setCwdPrefix("");
      setQuery("");
      setBackendFilter(ALL_BACKENDS);
    },
    cancelRunningImport: () => {
      if (!importing) return;
      canceledRef.current = true;
      ports.cancelImport?.(importing.requestId);
    },
    pickDirectory: () => {
      void ports.pickDirectory?.().then((picked) => {
        if (picked) setCwdOverride(picked);
      });
    },
  };
}
