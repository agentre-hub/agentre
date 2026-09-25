import * as React from "react";
import { Check, ShieldAlert, X } from "lucide-react";

import { Button } from "../../ui/button";
import { cn } from "../../lib/utils";
import { useUiTranslation } from "../../i18n";
import type { TranscriptBlockToolApproval } from "../dto";
import { useTranscriptPorts } from "../ports-context";
import {
  TranscriptCard,
  TranscriptCardBody,
  TranscriptPill,
} from "../transcript-card";

// CtlApprovalCard 渲染 toolKey="ctl" 的「变更清单卡」(spec 2026-09-22 决策 6,
// mockup CtlApprovalCard/ChangeRow)。agrctl 一次写命令对应一张卡:完整命令行
// + 每条变更的操作/类型/名字/字段前后值,密钥字段只写「已更新（值不显示）」,
// 带 --cascade 的删除写明连带删除的子部门/Agent 数。
//
// 与 ToolApprovalCard(旧 JSON 卡)不同:命令行 + 变更清单在任何 status 下都
// 渲染(方便已批准/已拒绝之后回看批的是什么),批准/拒绝按钮或结果文本另外
// 追加在下方——这与 mockup 的布局一致。
//
// ToolInput 的形状对应 Go 端 internal/pkg/transcript/blocks.CtlApprovalInput:
// { command, changes: [{ op, kind, id?, name, fields?: [{field,before?,after?,
// secret?}], cascade?: {departments,agents} }] }。id 在 JSON 里是数字
// (Go 侧 `json:"id,omitempty"`),create 时缺席。

export type CtlApprovalOp = "create" | "update" | "delete";

// Kind 是后端持久化的英文标识(agent/department/project/provider/model/backend);
// 展示态的本地化文案由 kindLabel() 查表得到,未知取值原样兜底显示。
export type CtlApprovalKind =
  | "agent"
  | "department"
  | "project"
  | "provider"
  | "model"
  | "backend"
  | (string & {});

export interface CtlApprovalField {
  field: string;
  before?: string;
  after?: string;
  secret?: boolean;
}

export interface CtlApprovalCascade {
  departments: number;
  agents: number;
}

export interface CtlApprovalChange {
  op: CtlApprovalOp;
  kind: CtlApprovalKind;
  id?: number;
  name: string;
  fields?: CtlApprovalField[];
  cascade?: CtlApprovalCascade;
}

export interface CtlApprovalInput {
  command: string;
  changes: CtlApprovalChange[];
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isCtlApprovalField(value: unknown): value is CtlApprovalField {
  if (!isPlainObject(value)) return false;
  if (typeof value.field !== "string") return false;
  if (value.before !== undefined && typeof value.before !== "string")
    return false;
  if (value.after !== undefined && typeof value.after !== "string")
    return false;
  if (value.secret !== undefined && typeof value.secret !== "boolean")
    return false;
  return true;
}

function isCtlApprovalCascade(value: unknown): value is CtlApprovalCascade {
  return (
    isPlainObject(value) &&
    typeof value.departments === "number" &&
    typeof value.agents === "number"
  );
}

function isCtlApprovalChange(value: unknown): value is CtlApprovalChange {
  if (!isPlainObject(value)) return false;
  if (value.op !== "create" && value.op !== "update" && value.op !== "delete")
    return false;
  if (typeof value.kind !== "string") return false;
  if (typeof value.name !== "string") return false;
  if (value.id !== undefined && typeof value.id !== "number") return false;
  if (value.fields !== undefined) {
    if (!Array.isArray(value.fields) || !value.fields.every(isCtlApprovalField))
      return false;
  }
  if (value.cascade !== undefined && !isCtlApprovalCascade(value.cascade))
    return false;
  return true;
}

// parseCtlApprovalInput 校验并还原 CtlApprovalInput;拿到旧格式/损坏数据时返回
// null,调用方(ToolApprovalCard 路由)据此兜底回旧 JSON 卡——不让一条读不懂的
// 审批卡变成白屏。
export function parseCtlApprovalInput(
  toolInput: Record<string, unknown> | undefined,
): CtlApprovalInput | null {
  if (!isPlainObject(toolInput)) return null;
  if (typeof toolInput.command !== "string") return null;
  if (!Array.isArray(toolInput.changes)) return null;
  if (!toolInput.changes.every(isCtlApprovalChange)) return null;
  return {
    command: toolInput.command,
    changes: toolInput.changes,
  };
}

const opPillClass: Record<CtlApprovalOp, string> = {
  create: "bg-status-running-bg text-status-running",
  update: "bg-status-waiting-bg text-status-waiting",
  delete: "bg-destructive/10 text-destructive",
};

function useCtlApprovalLabels() {
  const { t } = useUiTranslation();

  const opLabel = React.useCallback(
    (op: CtlApprovalOp): string => {
      switch (op) {
        case "create":
          return t("ctlApproval.op.create");
        case "update":
          return t("ctlApproval.op.update");
        case "delete":
          return t("ctlApproval.op.delete");
      }
    },
    [t],
  );

  const kindLabel = React.useCallback(
    (kind: CtlApprovalKind): string => {
      switch (kind) {
        case "agent":
          return t("ctlApproval.kind.agent");
        case "department":
          return t("ctlApproval.kind.department");
        case "project":
          return t("ctlApproval.kind.project");
        case "provider":
          return t("ctlApproval.kind.provider");
        case "model":
          return t("ctlApproval.kind.model");
        case "backend":
          return t("ctlApproval.kind.backend");
        default:
          return kind;
      }
    },
    [t],
  );

  return { opLabel, kindLabel };
}

// ChangeRow 渲染一条变更:操作徽标 + 类型 + 名字,展开的字段前后值,以及
// 级联删除的连带数量。密钥字段(secret===true)永远不渲染 before/after,
// 无论后端是否误把值带进来——前端多一层防线,不只信 Hard invariant 1。
function ChangeRow({ change }: { change: CtlApprovalChange }) {
  const { t } = useUiTranslation();
  const { opLabel, kindLabel } = useCtlApprovalLabels();

  return (
    <li className="flex flex-col gap-1 py-2 first:pt-0 last:pb-0">
      <div className="flex items-center gap-2">
        <TranscriptPill className={opPillClass[change.op]}>
          {opLabel(change.op)}
        </TranscriptPill>
        <span className="text-aux text-muted-foreground">
          {kindLabel(change.kind)}
        </span>
        <span className="font-mono text-sm font-medium">{change.name}</span>
      </div>
      {change.fields && change.fields.length > 0 && (
        <ul className="ml-1 flex flex-col gap-0.5 border-l pl-3 font-mono text-aux">
          {change.fields.map((field) => (
            <li key={field.field} className="flex flex-wrap gap-x-1.5">
              <span className="text-muted-foreground">{field.field}:</span>
              {field.secret ? (
                <span className="italic text-muted-foreground">
                  {t("ctlApproval.secretUpdated")}
                </span>
              ) : (
                <>
                  {field.before !== undefined && (
                    <>
                      <span className="text-muted-foreground line-through">
                        {field.before}
                      </span>
                      <span className="text-muted-foreground">→</span>
                    </>
                  )}
                  <span>{field.after}</span>
                </>
              )}
            </li>
          ))}
        </ul>
      )}
      {change.cascade && (
        <p className="ml-1 border-l pl-3 text-aux text-muted-foreground">
          {t("ctlApproval.cascade", {
            departments: change.cascade.departments,
            agents: change.cascade.agents,
          })}
        </p>
      )}
    </li>
  );
}

export interface CtlChangeListProps {
  changes: CtlApprovalChange[];
}

// CtlChangeList 是命令行之外的纯展示部分——只吃 changes,不碰会话/审批状态。
// 桌面端全局审批弹窗(外部调用,后续任务)与这张会话内卡片共用同一份渲染,
// 差的只是外壳(TranscriptCard vs. DialogShell)。
export function CtlChangeList({ changes }: CtlChangeListProps) {
  return (
    <ul className="flex flex-col divide-y">
      {changes.map((change) => (
        <ChangeRow
          key={change.kind + change.id + change.name}
          change={change}
        />
      ))}
    </ul>
  );
}

export interface CtlApprovalCardProps {
  approval: TranscriptBlockToolApproval;
  sessionId: number;
  input: CtlApprovalInput;
}

export function CtlApprovalCard({
  approval,
  sessionId,
  input,
}: CtlApprovalCardProps) {
  const { t } = useUiTranslation();
  const ports = useTranscriptPorts();
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const isPending = approval.status === "pending";
  const isApproved = approval.status === "approved";

  const answer = async (allow: boolean) => {
    if (!approval.requestId || submitting) return;
    setError(null);
    setSubmitting(true);
    try {
      // 同一个端口,和旧 JSON 卡走的是同一套唤醒机制,按 requestId 路由;
      // 接到哪个后端(桌面端 chat_svc / server 端 relay)由宿主决定。
      await ports.answerToolApproval({
        sessionId,
        requestId: approval.requestId,
        allow,
      });
    } catch {
      setError(t("toolApproval.submitFailed"));
      setSubmitting(false);
    }
  };

  return (
    <TranscriptCard
      data-testid="ctl-approval-card"
      data-selectable-text="true"
      className={cn(
        "text-card-foreground",
        !isPending && !isApproved
          ? "border-destructive/40"
          : "border-status-waiting/40",
      )}
    >
      <div className="flex items-center gap-2 px-3.5 py-2.5">
        <ShieldAlert
          className={cn(
            "h-4 w-4 shrink-0",
            isPending
              ? "text-status-waiting"
              : isApproved
                ? "text-status-running"
                : "text-destructive",
          )}
        />
        <span className="font-medium">{t("ctlApproval.title")}</span>
        {isPending && (
          <span className="text-aux text-muted-foreground">
            {t("ctlApproval.needsApproval")}
          </span>
        )}
        {!isPending && (
          <TranscriptPill
            data-copyable-control-text="true"
            className={cn(
              "ml-auto",
              isApproved
                ? "bg-status-running-bg text-status-running"
                : "bg-destructive/10 text-destructive",
            )}
          >
            {t(`toolApproval.status.${approval.status}`)}
          </TranscriptPill>
        )}
      </div>

      <TranscriptCardBody className="flex flex-col gap-2">
        <code className="w-fit max-w-full break-all rounded-sm bg-muted px-1.5 py-0.5 font-mono text-aux text-muted-foreground">
          {input.command}
        </code>
        <CtlChangeList changes={input.changes} />
      </TranscriptCardBody>

      {isPending ? (
        <TranscriptCardBody className="flex flex-wrap items-center gap-2">
          <Button size="sm" disabled={submitting} onClick={() => answer(true)}>
            <Check className="mr-1 h-3.5 w-3.5" />
            {t("toolApproval.approve")}
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={submitting}
            onClick={() => answer(false)}
          >
            <X className="mr-1 h-3.5 w-3.5" />
            {t("toolApproval.deny")}
          </Button>
          {error && <span className="text-aux text-destructive">{error}</span>}
        </TranscriptCardBody>
      ) : approval.result ? (
        <TranscriptCardBody className="text-aux text-muted-foreground">
          {approval.result}
        </TranscriptCardBody>
      ) : null}
    </TranscriptCard>
  );
}
