import * as React from "react";
import { Check, Link2, Plus, TriangleAlert } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";

import {
  Button,
  Popover,
  PopoverContent,
  PopoverTrigger,
  SearchInput,
  StatusDot,
  StatusMark,
  cn,
  formatRelativeTime,
  useUiTranslation,
  type StatusMarkState,
} from "@agentre-hub/agentre-ui";
import {
  IssueCreateLinked,
  IssueLinkSession,
  IssueSearchForSession,
  IssueUnlinkSession,
} from "@/../wailsjs/go/app/App";
import { useChatTabsStore } from "@/stores/chat-tabs-store";

import type { app, chat_svc } from "../../../../wailsjs/go/models";
import { formatElapsed } from "../background-tasks/background-tasks-popover";

import type { SessionRelations } from "./use-session-relations";

type Props = {
  sessionId: number;
  relations: SessionRelations;
};

/**
 * 派发状态到共享状态图标的对照：与子代理卡同一套词表（等待 / 运行中 / 已完成 / 失败 /
 * 已停止）——取消的派发在派发方转录的子代理卡上就是「已停止」。
 */
const DISPATCH_MARK: Record<string, StatusMarkState> = {
  queued: "pending",
  running: "running",
  completed: "done",
  failed: "failed",
  canceled: "stopped",
};

const ROW =
  "flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left text-xs outline-none transition-colors hover:bg-rail-accent focus-visible:focus-outline-inset";

function GroupHeading({ children }: { children: React.ReactNode }) {
  return (
    <h4 className="px-2 pt-3 pb-1 text-3xs font-semibold tracking-wide text-muted-foreground">
      {children}
    </h4>
  );
}

/** 派发方式的显示名；不认识的取值（新版本加的方式）原样显示。 */
function methodLabel(t: TFunction, method: string): string {
  switch (method) {
    case "agrctl_send":
      return t("relationsToolWindow.method.agrctl_send");
    case "agent_call":
      return t("relationsToolWindow.method.agent_call");
    default:
      return method;
  }
}

function openSession(sessionId: number) {
  useChatTabsStore.getState().openSessionInNewTab(sessionId);
}

/**
 * 「关联」工具窗：本会话关联的看板任务、派发我的、我派发的、同一份代码上在跑的其它
 * 会话（spec「关联工具窗」）。四组自上而下，空组整组不显示；全空时显示空态并保留
 * 「关联任务」「新建任务」两个按钮。
 */
export function RelationsToolWindow({ sessionId, relations }: Props) {
  const { t } = useTranslation();
  const { issues, dispatchedBy, dispatched, sharing, count, reload } =
    relations;

  const createLinked = React.useCallback(
    async (title: string) => {
      try {
        // 请求只是 JSON 形状：不依赖生成类的 createFrom。
        await IssueCreateLinked({
          sessionID: sessionId,
          title,
        } as app.IssueCreateLinkedRequest);
        reload();
        return true;
      } catch {
        toast.error(t("relationsToolWindow.failed"));
        return false;
      }
    },
    [sessionId, reload, t],
  );

  const taskActions = (
    <TaskActions
      sessionId={sessionId}
      onChanged={reload}
      onCreate={createLinked}
    />
  );

  return (
    <div className="min-h-0 flex-1 overflow-auto px-2 pb-2">
      {count === 0 ? (
        <>
          <p className="px-2 py-4 text-center text-xs text-muted-foreground">
            {t("relationsToolWindow.empty")}
          </p>
          {taskActions}
        </>
      ) : (
        <>
          {issues.length > 0 ? (
            <section>
              <GroupHeading>
                {t("relationsToolWindow.groups.tasks")}
              </GroupHeading>
              {issues.map((issue) => (
                <TaskRow key={issue.id} issue={issue} />
              ))}
            </section>
          ) : null}
          {taskActions}
          {dispatchedBy.length > 0 ? (
            <section>
              <GroupHeading>
                {t("relationsToolWindow.groups.dispatchedBy")}
              </GroupHeading>
              {dispatchedBy.map((item) => (
                <DispatchedByRow key={item.dispatchId} item={item} />
              ))}
            </section>
          ) : null}
          {dispatched.length > 0 ? (
            <section>
              <GroupHeading>
                {t("relationsToolWindow.groups.dispatched")}
              </GroupHeading>
              {dispatched.map((item) => (
                <DispatchedRow key={item.dispatchId} item={item} />
              ))}
            </section>
          ) : null}
          {sharing.length > 0 ? (
            <section>
              <GroupHeading>
                {t("relationsToolWindow.groups.sharing")}
              </GroupHeading>
              {sharing.map((item) => (
                <SharingRow key={item.sessionId} item={item} />
              ))}
            </section>
          ) : null}
        </>
      )}
    </div>
  );
}

function TaskRow({ issue }: { issue: app.SessionLinkedIssue }) {
  const { t } = useTranslation();
  const { t: ut } = useUiTranslation();
  const navigate = useNavigate();
  return (
    <button
      type="button"
      className={ROW}
      title={t("relationsToolWindow.openTask", { id: issue.id })}
      onClick={() => navigate(`/issues?issue=${issue.id}`)}
    >
      <span className="shrink-0 font-mono text-primary-text">#{issue.id}</span>
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="truncate text-foreground">{issue.title}</span>
        <span className="truncate text-2xs text-muted-foreground">
          {ut(`board.stages.${issue.stage}`)}
          {issue.otherSessionCount > 0
            ? ` · ${t("relationsToolWindow.otherSessions", { count: issue.otherSessionCount })}`
            : ""}
        </span>
      </span>
    </button>
  );
}

function DispatchedByRow({ item }: { item: chat_svc.DispatchedByItem }) {
  const { t } = useTranslation();
  const [now] = React.useState(() => Date.now());
  return (
    <button
      type="button"
      className={ROW}
      onClick={() => openSession(item.sessionId)}
    >
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="truncate text-foreground">{item.title}</span>
        <span className="truncate text-2xs text-muted-foreground">
          {[
            item.agentName,
            methodLabel(t, item.method),
            formatRelativeTime(item.createdAt, now, t),
          ]
            .filter(Boolean)
            .join(" · ")}
        </span>
      </span>
    </button>
  );
}

function DispatchedRow({ item }: { item: chat_svc.DispatchedItem }) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = React.useState(false);
  const replyId = React.useId();
  const state = DISPATCH_MARK[item.status] ?? "unknown";
  const onClick = item.isolated
    ? () => setExpanded((v) => !v)
    : () => openSession(item.sessionId);
  return (
    <div>
      <button
        type="button"
        className={ROW}
        aria-expanded={item.isolated ? expanded : undefined}
        aria-controls={item.isolated && expanded ? replyId : undefined}
        onClick={onClick}
      >
        <span className="mt-0.5 flex shrink-0">
          <StatusMark state={state} />
        </span>
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="truncate text-foreground">{item.task}</span>
          <span className="truncate text-2xs text-muted-foreground">
            {[
              item.agentName,
              methodLabel(t, item.method),
              item.isolated ? t("relationsToolWindow.isolated") : "",
              item.durationMs > 0 ? formatElapsed(item.durationMs) : "",
            ]
              .filter(Boolean)
              .join(" · ")}
          </span>
        </span>
      </button>
      {item.isolated && expanded ? (
        <div
          id={replyId}
          className="mx-2 mb-1 rounded-md bg-muted px-2 py-1.5 text-2xs text-muted-foreground select-text"
        >
          <div className="mb-0.5 font-semibold">
            {t("relationsToolWindow.lastReply")}
          </div>
          <p className="break-words whitespace-pre-wrap">
            {item.lastReply || t("relationsToolWindow.noReply")}
          </p>
        </div>
      ) : null}
    </div>
  );
}

function SharingRow({ item }: { item: chat_svc.SharingRootSession }) {
  const { t } = useTranslation();
  const alerting = item.overlapCount > 0;
  return (
    <button
      type="button"
      className={cn(
        ROW,
        alerting && "bg-status-waiting-bg hover:bg-status-waiting-bg",
      )}
      data-alert={alerting ? "true" : undefined}
      onClick={() => openSession(item.sessionId)}
    >
      <span className="mt-1 flex shrink-0">
        <StatusDot
          status={item.status === "waiting" ? "waiting" : "running"}
          size="xs"
        />
      </span>
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="truncate text-foreground">{item.title}</span>
        <span className="truncate text-2xs text-muted-foreground">
          {[item.agentName, item.rootName].filter(Boolean).join(" · ")}
        </span>
        {alerting ? (
          <span className="flex items-center gap-1 text-2xs text-status-waiting-text">
            <TriangleAlert className="size-3 shrink-0" aria-hidden="true" />
            <span className="truncate">
              {item.overlapCount > 1
                ? t("relationsToolWindow.overlapMany", {
                    file: item.overlapFirst,
                    count: item.overlapCount,
                  })
                : t("relationsToolWindow.overlapOne", {
                    file: item.overlapFirst,
                  })}
            </span>
          </span>
        ) : null}
      </span>
    </button>
  );
}

type TaskActionsProps = {
  sessionId: number;
  onChanged: () => void;
  /** 新建并关联；title 为空 = 用会话标题。成功交回 true。 */
  onCreate: (title: string) => Promise<boolean>;
};

/** 组下方的「关联任务」（搜索弹层）与「新建任务」两个按钮。 */
function TaskActions({ sessionId, onChanged, onCreate }: TaskActionsProps) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-wrap gap-1.5 px-2 py-2">
      <TaskPicker
        sessionId={sessionId}
        onChanged={onChanged}
        onCreate={onCreate}
      />
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => void onCreate("")}
      >
        <Plus aria-hidden="true" />
        {t("relationsToolWindow.newTask")}
      </Button>
    </div>
  );
}

/**
 * 「关联任务」弹层：搜索本会话所属项目及其子项目的任务（会话不属于项目时为全部），
 * 已关联的打勾，点击切换关联 / 取消关联；列表末尾「新建任务『搜索词』」。
 */
function TaskPicker({ sessionId, onChanged, onCreate }: TaskActionsProps) {
  const { t } = useTranslation();
  const { t: ut } = useUiTranslation();
  const [open, setOpen] = React.useState(false);
  const [keyword, setKeyword] = React.useState("");
  const [items, setItems] = React.useState<app.IssueLinkCandidate[]>([]);
  const seqRef = React.useRef(0);

  React.useEffect(() => {
    if (!open) return;
    seqRef.current += 1;
    const seq = seqRef.current;
    IssueSearchForSession(sessionId, keyword).then(
      (rows) => {
        if (seqRef.current === seq) setItems(rows ?? []);
      },
      () => {
        if (seqRef.current === seq) setItems([]);
      },
    );
  }, [open, sessionId, keyword]);

  const toggle = async (c: app.IssueLinkCandidate) => {
    try {
      if (c.linked) await IssueUnlinkSession(sessionId, c.id);
      else await IssueLinkSession(sessionId, c.id);
    } catch {
      toast.error(t("relationsToolWindow.failed"));
      return;
    }
    setItems((prev) =>
      prev.map((x) => (x.id === c.id ? { ...x, linked: !c.linked } : x)),
    );
    onChanged();
  };

  const term = keyword.trim();
  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setKeyword("");
      }}
    >
      <PopoverTrigger asChild>
        <Button type="button" variant="outline" size="sm">
          <Link2 aria-hidden="true" />
          {t("relationsToolWindow.linkTask")}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="flex w-72 flex-col gap-1 p-2">
        <SearchInput
          variant="bare"
          size="xs"
          value={keyword}
          onChange={setKeyword}
          placeholder={t("relationsToolWindow.searchTasks")}
          aria-label={t("relationsToolWindow.searchTasks")}
        />
        <div
          role="listbox"
          aria-label={t("relationsToolWindow.linkTask")}
          className="flex max-h-60 flex-col overflow-auto"
        >
          {items.map((c) => (
            <button
              key={c.id}
              type="button"
              role="option"
              aria-selected={c.linked}
              className={ROW}
              onClick={() => void toggle(c)}
            >
              <span className="flex size-3.5 shrink-0 items-center justify-center">
                {c.linked ? (
                  <Check
                    className="size-3.5 text-primary-text"
                    aria-hidden="true"
                    data-testid="task-linked-check"
                  />
                ) : null}
              </span>
              <span className="shrink-0 font-mono text-primary-text">
                #{c.id}
              </span>
              <span className="min-w-0 flex-1 truncate text-foreground">
                {c.title}
              </span>
              <span className="shrink-0 text-2xs text-muted-foreground">
                {ut(`board.stages.${c.stage}`)}
              </span>
            </button>
          ))}
        </div>
        {items.length === 0 ? (
          <p className="px-2 py-1 text-2xs text-muted-foreground">
            {t("relationsToolWindow.noMatch")}
          </p>
        ) : null}
        {term !== "" ? (
          <button
            type="button"
            className={ROW}
            onClick={() => {
              void onCreate(term).then((ok) => {
                if (ok) setOpen(false);
              });
            }}
          >
            <Plus className="size-3.5 shrink-0" aria-hidden="true" />
            <span className="truncate">
              {t("relationsToolWindow.createNamed", { term })}
            </span>
          </button>
        ) : null}
      </PopoverContent>
    </Popover>
  );
}
