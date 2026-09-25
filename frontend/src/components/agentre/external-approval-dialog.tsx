// ExternalApprovalDialog 是外部调用（非 TTY、无会话 token 的 agrctl 写命令）的桌面端全局
// 审批弹窗（docs/specs/2026-09-22-agrctl-resource-management.md「外部调用的审批弹窗
// （桌面端全局）」）。ctl_svc.DesktopApprovalQueue 每次变化（入队/作答/撤下——含 4 分钟
// 超时撤下）都经 CTL_EXTERNAL_APPROVAL_EVENT 推一份按入队顺序排列的快照；本组件永远
// 展示快照第一条，第一条被摘除（批准/拒绝/超时）后自动换下一条。
//
// 内容与会话内的 `ctl` 审批卡一致：完整命令行 + CtlChangeList（T5 已交付、
// 与宿主无关的变更清单渲染，会话卡也在用同一份），额外加一行调用方信息（agrctl 上报的
// 父进程名/pid/工作目录，只作提示）。窗口不在前台时同时发一条无会话的系统通知，点击后
// internal/app 的通知回调把窗口切回前台，本组件常驻挂载，弹窗随即可见。
//
// 关闭（× 或 Esc）与批准/拒绝共用同一条路径：都调 AnswerCtlApproval，区别只是 allow
// 的值——「关闭等同拒绝」不是本地状态，是真的替用户答了一次「拒绝」（spec 决策 12）。
import * as React from "react";
import { useTranslation } from "react-i18next";
import { Check, ShieldAlert, Terminal, X } from "lucide-react";

import {
  Button,
  CtlChangeList,
  DialogShell,
  DialogShellBody,
  DialogShellFooter,
  DialogShellHeader,
} from "@agentre-hub/agentre-ui";
import type { CtlChangeListProps } from "@agentre-hub/agentre-ui";

import {
  AnswerCtlApproval,
  PendingCtlApprovals,
  ShowNotification,
} from "../../../wailsjs/go/app/App";
import { EventsOn } from "../../../wailsjs/runtime/runtime";
import { isWindowFocused } from "@/lib/window-focus";

// 与 internal/app.CtlExternalApprovalEvent 同名；那边是常量，这边只有这一处引用
// （与 sync-applied-host.tsx 的既有约定一致）。
const CTL_EXTERNAL_APPROVAL_EVENT = "ctl:external-approval";

interface ExternalApprovalCaller {
  parentProcess: string;
  pid: number;
  workingDir: string;
}

// 与 ctl_svc.DesktopApprovalItem 的 JSON 形状一致。changes 的元素形状与 CtlChangeList
// 吃的 CtlApprovalChange 完全同构，直接用它的 props 类型取，不用另外声明一遍
// （也因此这个文件不需要从 index.ts 再拿 CtlApprovalChange）。
interface ExternalApprovalItem {
  requestId: string;
  command: string;
  changes: CtlChangeListProps["changes"];
  caller?: ExternalApprovalCaller;
}

function isDeleteItem(item: ExternalApprovalItem): boolean {
  return item.changes.some((c) => c.op === "delete");
}

export function ExternalApprovalDialog(): React.ReactElement | null {
  const { t } = useTranslation();
  const [items, setItems] = React.useState<ExternalApprovalItem[]>([]);
  const prevCountRef = React.useRef(0);

  React.useEffect(() => {
    // 先订阅、再补拉一次当前队列：订阅之前入队的请求（启动早期、webview 重载）不会
    // 再有事件。推送一旦先到，就以推送为准——补拉的结果已经过时。
    let pushed = false;
    const off = EventsOn(
      CTL_EXTERNAL_APPROVAL_EVENT,
      (payload?: ExternalApprovalItem[]) => {
        pushed = true;
        const next = payload ?? [];
        if (next.length > prevCountRef.current && !isWindowFocused()) {
          const newest = next[next.length - 1];
          if (newest) {
            ShowNotification({
              title: t("externalApproval.notification.title"),
              body: t("externalApproval.notification.body", {
                process:
                  newest.caller?.parentProcess ??
                  t("externalApproval.unknownCaller"),
              }),
              sessionId: 0,
            }).catch(() => {});
          }
        }
        prevCountRef.current = next.length;
        setItems(next);
      },
    );
    let cancelled = false;
    PendingCtlApprovals()
      .then((initial) => {
        if (cancelled || pushed) return;
        const next = (initial ?? []) as unknown as ExternalApprovalItem[];
        prevCountRef.current = next.length;
        setItems(next);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
      if (typeof off === "function") off();
    };
  }, [t]);

  // 作答成功就先在本地摘掉这一条，不等下一份快照——快照万一没到，弹窗也不会卡在
  // 按钮全禁用的状态里。随后到达的快照照常覆盖本地列表。
  const answered = React.useCallback((requestId: string) => {
    setItems((xs) => xs.filter((x) => x.requestId !== requestId));
  }, []);

  const current = items[0];
  if (!current) return null;

  return (
    <ExternalApprovalDialogBody
      key={current.requestId}
      item={current}
      total={items.length}
      onAnswered={answered}
    />
  );
}

function ExternalApprovalDialogBody({
  item,
  total,
  onAnswered,
}: {
  item: ExternalApprovalItem;
  total: number;
  onAnswered: (requestId: string) => void;
}) {
  const { t } = useTranslation();
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const isDelete = isDeleteItem(item);

  const answer = (allow: boolean) => {
    if (submitting) return;
    setSubmitting(true);
    setError(null);
    AnswerCtlApproval(item.requestId, allow)
      .then(() => onAnswered(item.requestId))
      .catch(() => {
        // Wails 以 Go 的原始错误串拒绝（请求已撤下 / 已答过），不上界面。
        setSubmitting(false);
        setError(t("externalApproval.submitFailed"));
      });
  };

  return (
    <DialogShell
      open
      onOpenChange={(open) => {
        if (!open) answer(false);
      }}
      size="md"
      danger={isDelete}
      busy={submitting}
    >
      <DialogShellHeader
        title={
          <span className="flex items-center gap-2">
            <ShieldAlert
              className={
                "size-[18px] " +
                (isDelete ? "text-destructive" : "text-status-waiting")
              }
            />
            {t("externalApproval.title")}
          </span>
        }
        subtitle={
          total > 1
            ? t("externalApproval.queuePosition", { index: 1, total })
            : undefined
        }
        danger={isDelete}
        busy={submitting}
        onClose={() => answer(false)}
      />
      <DialogShellBody className="flex flex-col gap-3">
        {item.caller ? (
          <div className="flex items-center gap-2 rounded-md border bg-muted/40 px-3 py-2 text-aux">
            <Terminal className="size-3.5 text-muted-foreground" />
            <span className="font-medium">{item.caller.parentProcess}</span>
            <span className="text-muted-foreground">
              {t("externalApproval.callerPid", { pid: item.caller.pid })}
            </span>
            <span className="ml-auto truncate font-mono text-muted-foreground">
              {item.caller.workingDir}
            </span>
          </div>
        ) : null}
        <code className="w-fit max-w-full break-all rounded-sm bg-muted px-1.5 py-0.5 font-mono text-aux text-muted-foreground">
          {item.command}
        </code>
        <CtlChangeList changes={item.changes} />
        <p className="text-aux text-muted-foreground">
          {t("externalApproval.autoRejectHint")}
        </p>
      </DialogShellBody>
      <DialogShellFooter error={error}>
        <Button
          variant="outline"
          onClick={() => answer(false)}
          disabled={submitting}
        >
          <X className="mr-1 h-3.5 w-3.5" />
          {t("externalApproval.reject")}
        </Button>
        <Button
          variant={isDelete ? "destructive" : "default"}
          onClick={() => answer(true)}
          disabled={submitting}
        >
          <Check className="mr-1 h-3.5 w-3.5" />
          {isDelete
            ? t("externalApproval.approveDelete")
            : t("externalApproval.approve")}
        </Button>
      </DialogShellFooter>
    </DialogShell>
  );
}
