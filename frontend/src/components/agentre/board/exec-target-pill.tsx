import * as React from "react";
import { MapPin, Server, ServerOff } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import {
  backendPrimaryName,
  backendTypeLabel,
  deviceLabelOf,
  reasonLabel,
  useExecTargetCandidates,
  type ExecTargetCandidate,
} from "../session-exec-target";

export type BoardExecTargetPillProps = {
  /** 共享包递过来的 pill 形状；三颗触发器摆在一排必须是同一串。 */
  className: string;
  agentId: number;
  /** `null` = 未归属；那台机器上这个项目的路径就是靠它算的。 */
  projectId: number | null;
  backendId: number | null;
  onChange: (backendId: number | null) => void;
  /**
   * 生效档解析出来的后端类型（没选 / 解不出来 = 空串）。模型那一颗的兼容判据要的
   * 就是它 —— 「跟随 Agent 绑定」时才轮到 Agent 自己那个后端类型说话。
   */
  onResolvedBackendType?: (backendType: string) => void;
  disabled?: boolean;
};

function MachineIcon({ candidate }: { candidate: ExecTargetCandidate }) {
  if (!candidate.deviceId) {
    return <MapPin className="size-3 shrink-0" aria-hidden="true" />;
  }
  return candidate.online ? (
    <Server className="size-3 shrink-0" aria-hidden="true" />
  ) : (
    <ServerOff className="size-3 shrink-0" aria-hidden="true" />
  );
}

/**
 * 任务表单执行段的**机器**那一颗。
 *
 * 候选与可用性判定复用会话侧的 `useExecTargetCandidates`（Wails + `remote.device.state`
 * 推送）—— 这是宿主代码，共享包只经端口拿到画好的这一颗。不可用的档**保留在列表里
 * 并说明原因**：把它藏掉的话，用户只会得出「那台机器不存在」这个错误结论。
 */
export function BoardExecTargetPill({
  className,
  agentId,
  projectId,
  backendId,
  onChange,
  onResolvedBackendType,
  disabled,
}: BoardExecTargetPillProps) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const { candidates, loaded } = useExecTargetCandidates(
    agentId,
    projectId ?? 0,
  );

  const selected = backendId
    ? candidates.find(
        (candidate) =>
          candidate.agentBackendId === backendId && candidate.available,
      )
    : undefined;

  // 换了 Agent 或项目之后这一档可能已经不在候选里（或不再可用）：退回「跟随 Agent
  // 绑定」，不留一个死指向。只在候选**真的加载过**之后才判 —— 还没拉到 / 这次拉
  // 失败时 candidates 同样是空的，此时清掉等于把用户刚选的机器悄悄换掉。
  React.useEffect(() => {
    if (!loaded) return;
    if (backendId && !selected) onChange(null);
  }, [backendId, loaded, onChange, selected]);

  // 生效档的后端类型往上报一次：模型那一颗要用它过 `isProviderCompatible`，而候选
  // 只有这里有。换机器 / 换 Agent / 换项目都会重解析，报的始终是此刻真生效的那一档。
  React.useEffect(() => {
    onResolvedBackendType?.(selected?.backendType ?? "");
  }, [onResolvedBackendType, selected]);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          data-testid="board-exec-target-pill"
          disabled={disabled}
          className={className}
          aria-label={t("issues.exec.machineAria")}
        >
          {selected ? <MachineIcon candidate={selected} /> : null}
          <span
            className={cn("truncate", !selected && "text-muted-foreground")}
          >
            {selected
              ? backendPrimaryName(selected, t)
              : t("issues.exec.followAgent")}
          </span>
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-72 p-0">
        <div className="border-b border-border px-3 py-2 text-2xs font-semibold">
          {t("issues.exec.pickerTitle")}
        </div>
        <div className="max-h-64 overflow-y-auto">
          <button
            type="button"
            data-testid="board-exec-target-row-follow"
            onClick={() => {
              onChange(null);
              setOpen(false);
            }}
            className="flex w-full cursor-pointer items-center gap-2 border-b border-border px-3 py-2 text-left text-xs transition-colors hover:bg-accent"
          >
            {t("issues.exec.followAgent")}
          </button>
          {candidates.map((candidate) => (
            <button
              key={candidate.agentBackendId}
              type="button"
              data-testid={`board-exec-target-row-${candidate.agentBackendId}`}
              disabled={!candidate.available}
              onClick={() => {
                onChange(candidate.agentBackendId);
                setOpen(false);
              }}
              className={cn(
                "flex w-full items-start gap-2 border-b border-border px-3 py-2 text-left last:border-b-0",
                candidate.available
                  ? "cursor-pointer hover:bg-accent"
                  : "cursor-not-allowed opacity-60",
              )}
            >
              <span className="mt-0.5 text-muted-foreground">
                <MachineIcon candidate={candidate} />
              </span>
              <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                <span className="truncate text-xs font-semibold">
                  {backendPrimaryName(candidate, t)}
                </span>
                {/* 「这一档是什么」：种类 · 后端类型 · 哪台机器。 */}
                <span className="truncate text-2xs text-muted-foreground">
                  {[
                    t(`issues.exec.kinds.${candidate.kind}`),
                    backendTypeLabel(candidate.backendType),
                    deviceLabelOf(candidate, t),
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                </span>
                {candidate.projectPath ? (
                  <span
                    title={candidate.projectPath}
                    className="truncate font-mono text-2xs text-muted-foreground"
                  >
                    {candidate.projectPath}
                  </span>
                ) : null}
                {!candidate.available ? (
                  <span className="truncate text-2xs text-status-waiting">
                    {reasonLabel(candidate.reason, t)}
                  </span>
                ) : null}
              </span>
            </button>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  );
}
