import * as React from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { ChevronDown, MapPin, Server, ServerOff } from "lucide-react";
import { cn } from "@/lib/utils";

import {
  ListAgentBackends,
  ListAgentExecTargetAvailability,
  ServerListDevices,
} from "../../../wailsjs/go/app/App";
import type { agent_backend_svc } from "../../../wailsjs/go/models";
import { EventsOn } from "../../../wailsjs/runtime/runtime";

import { navigateToTarget } from "./not-chattable";

// ── 数据：把 R15 的可用性判定(ListAgentExecTargetAvailability)与全量后端列表
//    (ListAgentBackends)按 agentBackendId 拼接成"改选"浮层/空会话态一行要用的候选
//    列表——两个都是既有的通用 wails 绑定(非组织架构页私有)，顺序沿用可用性接口的
//    返回顺序(即执行目标表的 sort_order)。────────────────────────────────────

export type ExecTargetCandidate = {
  agentBackendId: number;
  deviceId: string;
  deviceName: string;
  online: boolean;
  backendType: string;
  backendName: string;
  llmProviderKey: string;
  llmModelKey: string;
  available: boolean;
  reason: string;
  hint: string;
  // kind: 这一档的目标种类（R18）—— "local" 本机 / "desktop" 另一台桌面端（派发走
  // peer 中继）/ "daemon" agentred。由后端 ListAgentExecTargetAvailability 逐档给出。
  kind: "local" | "desktop" | "daemon";
  // projectPath: 这一档所在的机器上、这个项目的路径(本机档取 projects.path，
  // agentred 档取 project_locations 里那一行)。会话不绑项目或那台机器上没配时
  // 为空串——选机器时真正要判断的是"换过去在哪个目录干活"，比机器名更有信息量。
  projectPath: string;
};

// REMOTE_DEVICE_STATE_EVENT 是 remote_device_watcher_svc 的在线态推送通道
// (Go 侧 remote_device_watcher_svc.EventName)，use-remote-devices 订的是同一条。
const REMOTE_DEVICE_STATE_EVENT = "remote.device.state";

export function useExecTargetCandidates(agentId: number, projectId: number) {
  const [candidates, setCandidates] = React.useState<ExecTargetCandidate[]>([]);
  const [loading, setLoading] = React.useState(false);
  // loaded 区分「候选真的是空的」与「还没拉到 / 这次拉失败了」——两种情况下
  // candidates 都是 []，但只有前者能据以判断某一档确实没了。
  const [loaded, setLoaded] = React.useState(false);
  // 只有最新一次请求可以写状态：设备上下线推送与 agentId/projectId 变化都会让多次
  // reload 重叠，先发的那次晚返回时不能把新快照盖回旧的（否则用户看着「将在 X 上
  // 运行」按下回车、实际跑到了别处——正是这个订阅要防的那件事）。卸载时也把代次推
  // 进一格，在飞的请求回来后不再写已卸载组件的状态。
  const reqRef = React.useRef(0);

  const reload = React.useCallback(async () => {
    const req = ++reqRef.current;
    if (!agentId) {
      setCandidates([]);
      setLoaded(false);
      return;
    }
    setLoading(true);
    try {
      const [availability, backends, accountDevices] = await Promise.all([
        ListAgentExecTargetAvailability(agentId, projectId),
        ListAgentBackends(),
        ServerListDevices().catch(() => []),
      ]);
      if (req !== reqRef.current) return;
      const backendById = new Map<number, agent_backend_svc.BackendItem>();
      for (const b of backends?.items ?? []) backendById.set(b.id, b);
      const accountNameByFingerprint = new Map(
        (accountDevices ?? [])
          .filter((device) => device.Fingerprint)
          .map((device) => [device.Fingerprint, device.Name] as const),
      );
      setCandidates(
        (availability ?? []).map((a) => {
          const b = backendById.get(a.agentBackendId);
          return {
            agentBackendId: a.agentBackendId,
            deviceId: b?.deviceId ?? "",
            deviceName:
              b?.deviceName ||
              accountNameByFingerprint.get(b?.deviceId ?? "") ||
              "",
            online: b?.online ?? false,
            backendType: b?.type ?? "",
            backendName: b?.name ?? "",
            llmProviderKey: b?.llmProviderKey ?? "",
            llmModelKey: b?.llmModelKey ?? "",
            available: a.available,
            reason: a.reason,
            hint: a.hint,
            projectPath: a.projectPath,
            kind: (a.kind as ExecTargetCandidate["kind"]) ?? "daemon",
          };
        }),
      );
      setLoaded(true);
    } catch (e) {
      if (req !== reqRef.current) return;
      console.error("[chat] exec target candidates load failed", e);
      setCandidates([]);
      setLoaded(false);
    } finally {
      if (req === reqRef.current) setLoading(false);
    }
    // projectId 变化也要重新拉——"这台机器上有没有配这个项目的路径"这一项判据
    // 只在绑定项目时生效。
  }, [agentId, projectId]);

  React.useEffect(() => {
    void reload();
  }, [reload]);

  React.useEffect(
    () => () => {
      reqRef.current++;
    },
    [],
  );

  // 选中结果在起轮前是活的(R15a)：空会话态期间可用性变了就重新算，否则用户看着
  // "将在 X 上运行"按下回车、实际跑到了别处。信号取既有的 remote.device.state
  // 推送(agentred 上下线正是可用性判据里唯一会自己翻转的那一项)，不新造轮询；
  // 这个 hook 只在空会话态那一行里挂载，订阅随它一起卸载。
  React.useEffect(() => {
    const off = EventsOn(REMOTE_DEVICE_STATE_EVENT, () => {
      void reload();
    });
    return () => off?.();
  }, [reload]);

  return { candidates, loading, loaded, reload };
}

const REASON_I18N_KEY: Record<string, string> = {
  "no-backend": "orphanBackend",
  "backend-requires-provider": "backendRequiresProvider",
  "provider-inactive": "providerInactive",
  "remote-provider-missing": "remoteProviderMissing",
  "gateway-not-running": "gatewayNotRunning",
  "remote-openclaw-unavailable": "remoteOpenclawUnavailable",
  "unknown-backend": "unknownBackend",
  "exec-target-unpaired": "unpaired",
  "exec-target-offline": "offline",
  "exec-target-desktop-not-running": "desktopNotRunning",
  "exec-target-project-path-missing": "projectPathMissing",
};

function reasonLabel(reason: string, t: TFunction): string {
  const key = REASON_I18N_KEY[reason] ?? "unknownBackend";
  return t(`chatPanel.execTarget.reasons.${key}`);
}

const BACKEND_TYPE_LABEL: Record<string, string> = {
  claudecode: "Claude Code",
  "claude-code": "Claude Code",
  codex: "Codex",
  builtin: "Built-in",
  openclaw: "OpenClaw",
  piagent: "Pi Agent",
};

function backendTypeLabel(type: string): string {
  return BACKEND_TYPE_LABEL[type] ?? type;
}

// 后端主位名字：Agent 后端名字（用户在后端管理页起的名字）；缺名时回落到类型
// 标签，再不行兜底"本机/本地"。
function backendPrimaryName(c: ExecTargetCandidate, t: TFunction): string {
  return (
    c.backendName || backendTypeLabel(c.backendType) || t("deviceTag.local")
  );
}

// 设备标签：本机档 → 本机（本地）；远端档 → 设备名（缺名用 id）。
function deviceLabelOf(c: ExecTargetCandidate, t: TFunction): string {
  if (!c.deviceId) return t("deviceTag.local");
  return c.deviceName || c.deviceId;
}

// 平铺文本（掉档措辞 / 全不可用面板用）：主位是 Agent 后端名字，远端档追加
// "· 设备名"。chip 与改选浮层各自结构化渲染（后端名 + 设备分行/分块），不用它。
function targetLabel(c: ExecTargetCandidate, t: TFunction): string {
  const name = backendPrimaryName(c, t);
  if (!c.deviceId) return name;
  return `${name} · ${deviceLabelOf(c, t)}`;
}

// ── 空会话态「将在 X 上运行」（R15a），X 是可点击的 chip，按下打开改选浮层
//    （不再有独立的"改选"按钮）。只有一个（可用性判定成功返回长度<=1）候选时
//    整行不出现，视觉上等价于今天什么都不显示。──────────────────────────────

export type NewSessionExecTargetLineProps = {
  agentId: number;
  agentName: string;
  projectId: number;
  overrideBackendId: number | null;
  onOverride: (agentBackendId: number | null) => void;
  /** 把改选后实际生效档的 backend type 报给父级（改选场景非空）。父级据此推导
      permission mode caps —— 空会话态改选到另一个类型的后端后，mode 的 allowed
      集合/默认值必须跟随实际后端，否则首发会带上旧类型才合法的 mode。 */
  onOverrideBackendType?: (backendType: string | null) => void;
  /** 把改选后实际生效档的目标种类与设备身份报给父级（R18 桌面派发）。父级在首发时
      据此决定走本地 Send 还是 peer RunFresh。 */
  onEffectiveTarget?: (
    target: {
      kind: "local" | "desktop" | "daemon";
      deviceId: string;
      deviceName: string;
      backendType: string;
      llmProviderKey: string;
      llmModelKey: string;
    } | null,
  ) => void;
};

export function NewSessionExecTargetLine(props: NewSessionExecTargetLineProps) {
  const { t } = useTranslation();
  const { candidates, loaded } = useExecTargetCandidates(
    props.agentId,
    props.projectId,
  );

  // 改选浮层的开合状态由本组件持有：选中某一档后立即关闭（用户点完即可直接发问，
  // 不用再点外部/Escape）。Radix Popover 默认不受控，内容里的点击不会自动收起。
  const [pickerOpen, setPickerOpen] = React.useState(false);

  const defaultCandidate = candidates.find((c) => c.available);
  const overrideCandidate = props.overrideBackendId
    ? candidates.find(
        (c) => c.agentBackendId === props.overrideBackendId && c.available,
      )
    : undefined;
  const effective = overrideCandidate ?? defaultCandidate;
  // 手动指定的档一旦不再可用，回落到自动挑选——不留一个死指向。这个 effect 必须
  // 在任何 early return 之前调用（Rules of Hooks），所以放在整个组件的顶部。
  // 只在候选列表真的成功加载过之后才判：还没拉到 / 这次拉失败时 candidates 同样是
  // 空的，此时清掉手动指定，会把用户刚选的机器悄悄换回自动挑选的那一档。
  React.useEffect(() => {
    if (!loaded) return;
    if (props.overrideBackendId && !overrideCandidate) {
      props.onOverride(null);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loaded, props.overrideBackendId, overrideCandidate]);

  // 把「改选后实际生效档的 backend type」报给父级：父级据此推导 permission mode
  // caps / pill。改选到另一个类型的后端时（如 claudecode 主后端 → codex 后端），
  // 不跟随的话首发会带上旧类型才合法的 mode（acceptEdits/bypassPermissions）被后端
  // 拒绝；跟随后 pill 只显示新后端的 mode 集合，Send payload 也带合法值。
  React.useEffect(() => {
    props.onOverrideBackendType?.(overrideCandidate?.backendType ?? null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.onOverrideBackendType, overrideCandidate?.backendType]);

  // 把改选后实际生效档的目标种类与设备身份报给父级（R18）：父级据此在首发时决定
  // 走本地 Send 还是 peer RunFresh。无生效档（候选为空/全不可用）报 null。
  React.useEffect(() => {
    props.onEffectiveTarget?.(
      effective
        ? {
            kind: effective.kind,
            deviceId: effective.deviceId,
            deviceName: effective.deviceName,
            backendType: effective.backendType,
            llmProviderKey: effective.llmProviderKey,
            llmModelKey: effective.llmModelKey,
          }
        : null,
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.onEffectiveTarget, effective]);

  if (candidates.length <= 1) return null;

  if (!effective) {
    return (
      <div
        data-testid="new-session-exec-target-all-unavailable"
        className="mt-2 flex max-w-md gap-2 rounded-md border border-destructive bg-destructive-soft px-3 py-2.5 text-left"
      >
        <div className="flex min-w-0 flex-col gap-1">
          <span className="text-xs font-semibold">
            {t("chatPanel.execTarget.allUnavailableTitle", {
              name: props.agentName,
            })}
          </span>
          <span className="text-2xs leading-relaxed text-muted-foreground">
            {candidates.map((c, i) => (
              <React.Fragment key={c.agentBackendId}>
                {i + 1} · {targetLabel(c, t)} —— {reasonLabel(c.reason, t)}
                <br />
              </React.Fragment>
            ))}
          </span>
        </div>
      </div>
    );
  }

  // "掉档"：不是手动指定、而是自动挑选跳过了排最前的档，唯一需要加重的一处。
  const dropped =
    !overrideCandidate &&
    candidates.length > 0 &&
    effective.agentBackendId !== candidates[0].agentBackendId;

  return (
    <div
      data-testid="new-session-exec-target-line"
      className={cn(
        "mt-2 flex flex-wrap items-center justify-center gap-1.5 rounded-md px-2 py-1",
        dropped && "bg-status-waiting-bg",
      )}
    >
      {dropped ? (
        <span className="text-2xs text-muted-foreground">
          {t("chatPanel.execTarget.droppedPrefix", {
            name: targetLabel(candidates[0], t),
          })}
        </span>
      ) : (
        <span className="text-2xs text-muted-foreground">
          {t("chatPanel.execTarget.willRunPrefix")}
        </span>
      )}
      {/* "会话跑在哪台机器"：chip 本身可点（打开改选浮层），不再另放一个
          "改选"按钮。主位显示 Agent 后端名字（不再渲染"本机/本地"机器措辞），
          远端档把设备名以弱化小字缀在后头（不再用"× 设备名"）。chip 的配色/图
          标仍按机器归属走，与命令面板/聊天头/成员页保持同一套视觉；掉档的加重
          由整行底色 + 措辞 + 原因承担。 */}
      <Popover open={pickerOpen} onOpenChange={setPickerOpen}>
        <PopoverTrigger asChild>
          <ExecTargetChipButton
            candidate={effective}
            ariaLabel={t("chatPanel.execTarget.pickChipAria", {
              label: targetLabel(effective, t),
            })}
          />
        </PopoverTrigger>
        <PopoverContent align="center" className="w-72 p-0">
          <ExecTargetReselectPopover
            agentId={props.agentId}
            candidates={candidates}
            selectedId={effective.agentBackendId}
            onPick={(id) => {
              props.onOverride(id);
              setPickerOpen(false);
            }}
          />
        </PopoverContent>
      </Popover>
      <span className="text-2xs text-muted-foreground">
        {t("chatPanel.execTarget.willRunSuffix")}
      </span>
      {dropped && candidates[0] && (
        <p className="w-full text-center text-2xs text-muted-foreground">
          {reasonLabel(candidates[0].reason, t)}
        </p>
      )}
    </div>
  );
}

// 空会话态「将在 X 上运行」的可点击 chip：按下打开改选浮层（不再有独立的"改选"
// 按钮）。主位显示 Agent 后端名字，远端档把设备名以弱化的小字缀在后头（不再用
// "× 设备名"）；配色/图标沿用共享 DeviceTag 的机器归属语义：本机 → MapPin/
// primary，远端在线 → Server/running，远端离线 → ServerOff/muted。
// Radix Slot 会把 PopoverTrigger 的 onClick/ref 等合并进 asChild 的子元素，所以
// 这里必须 forwardRef 并把剩余 props 透传到 <button> 上，否则弹层永远打不开。
const ExecTargetChipButton = React.forwardRef<
  HTMLButtonElement,
  {
    candidate: ExecTargetCandidate;
    ariaLabel: string;
  } & React.ComponentProps<"button">
>(function ExecTargetChipButton(
  { candidate, ariaLabel, className, ...rest },
  ref,
) {
  const { t } = useTranslation();
  const c = candidate;
  const remote = Boolean(c.deviceId);
  const tone = !remote
    ? "bg-primary-soft text-primary"
    : c.online
      ? "bg-status-running-bg text-status-running"
      : "bg-muted text-muted-foreground";
  return (
    <button
      ref={ref}
      type="button"
      data-testid="new-session-exec-target-chip"
      aria-label={ariaLabel}
      className={cn(
        "inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 text-2xs font-medium transition-colors underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
        tone,
        className,
      )}
      {...rest}
    >
      {!remote ? (
        <MapPin className="size-3" aria-hidden="true" />
      ) : c.online ? (
        <Server className="size-3" aria-hidden="true" />
      ) : (
        <ServerOff className="size-3" aria-hidden="true" />
      )}
      <span>{backendPrimaryName(c, t)}</span>
      {remote && (
        <>
          <span className="opacity-60" aria-hidden="true">
            ·
          </span>
          <span className="opacity-80">{deviceLabelOf(c, t)}</span>
        </>
      )}
      <ChevronDown className="size-3 opacity-60" aria-hidden="true" />
    </button>
  );
});

// ExecTargetReselectPopover 只在弹层实际打开时挂载（Radix Popover 默认关闭态不
// 渲染 content），useNavigate 因此放在这里而不是 NewSessionExecTargetLine
// 顶层——后者在任何新会话态都会渲染，很多既有测试没有包 <MemoryRouter>，
// 提前拿 navigate 会在这些测试里直接抛错。
function ExecTargetReselectPopover(props: {
  agentId: number;
  candidates: ExecTargetCandidate[];
  selectedId: number;
  onPick: (agentBackendId: number) => void;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const onGoAdjust = () => {
    navigateToTarget(navigate, "org-agent:<id>", props.agentId);
  };
  return (
    <div data-testid="exec-target-picker">
      <div className="flex items-baseline justify-between gap-2 border-b border-border px-3 py-2">
        <span className="text-2xs font-semibold">
          {t("chatPanel.execTarget.pickerTitle")}
        </span>
        <span className="text-2xs text-muted-foreground">
          {t("chatPanel.execTarget.pickerScope")}
        </span>
      </div>
      {props.candidates.map((c) => (
        <button
          key={c.agentBackendId}
          type="button"
          disabled={!c.available}
          onClick={() => props.onPick(c.agentBackendId)}
          className={cn(
            "flex w-full items-start gap-2 border-b border-border px-3 py-2 text-left last:border-b-0",
            c.available ? "hover:bg-accent" : "cursor-not-allowed opacity-60",
          )}
        >
          <span
            className={cn(
              "mt-1 size-3.5 shrink-0 rounded-full border-[1.5px]",
              c.agentBackendId === props.selectedId
                ? "border-primary shadow-[inset_0_0_0_3px_var(--primary)]"
                : "border-border-strong",
            )}
            aria-hidden="true"
          />
          <div className="flex min-w-0 flex-1 flex-col gap-0.5">
            <span className="text-xs font-semibold">
              {backendPrimaryName(c, t)}
            </span>
            {/* 这一档在哪台机器上跑：图标 + 设备名（本机/远端），比"名 × 设备"
                一行平铺更易扫读。 */}
            <span className="flex items-center gap-1 text-2xs text-muted-foreground">
              {!c.deviceId ? (
                <MapPin className="size-3" aria-hidden="true" />
              ) : c.online ? (
                <Server className="size-3" aria-hidden="true" />
              ) : (
                <ServerOff className="size-3" aria-hidden="true" />
              )}
              <span>{deviceLabelOf(c, t)}</span>
            </span>
            {!c.available && (
              <span className="font-mono text-2xs text-muted-foreground">
                {reasonLabel(c.reason, t)}
              </span>
            )}
            {c.projectPath ? (
              <span
                data-testid="exec-target-project-path"
                title={c.projectPath}
                className="truncate font-mono text-2xs text-muted-foreground"
              >
                {c.projectPath}
              </span>
            ) : null}
          </div>
        </button>
      ))}
      <div className="flex items-center justify-between gap-2 bg-muted px-3 py-2">
        <span className="text-2xs text-muted-foreground">
          {t("chatPanel.execTarget.stickyHint")}
        </span>
        <button
          type="button"
          onClick={onGoAdjust}
          className="text-2xs text-primary-text underline-offset-2 hover:underline"
        >
          {t("chatPanel.execTarget.goAdjust")}
        </button>
      </div>
    </div>
  );
}
