import { MessageSquare } from "lucide-react";
import { useTranslation } from "react-i18next";

import { StatusDot } from "@agentre-hub/agentre-ui";
import type { chat_svc } from "@/../wailsjs/go/models";
import type { LiveStream } from "@/stores/chat-streams-store";

/** 最后一条用户消息的正文（文字块拼起来）；还没有用户消息、或只有图片时为 null。 */
export function lastUserMessageText(
  messages: readonly Pick<chat_svc.ChatMessage, "role" | "blocks">[],
): string | null {
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    if (m.role !== "user") continue;
    const text = (m.blocks ?? [])
      .filter((b) => b.type === "text" && b.text)
      .map((b) => b.text)
      .join("\n")
      .trim();
    if (text !== "") return text;
  }
  return null;
}

// 工具入参里最能说明「它在动什么」的那一项，按这个顺序取第一个非空字符串。
const TARGET_KEYS = [
  "file_path",
  "notebook_path",
  "path",
  "command",
  "pattern",
  "url",
  "query",
  "description",
] as const;

function toolTarget(
  input: Record<string, unknown> | undefined,
  cwd: string,
): string {
  if (!input) return "";
  for (const key of TARGET_KEYS) {
    const v = input[key];
    if (typeof v !== "string" || v.trim() === "") continue;
    const line = v.trim().split("\n", 1)[0];
    const prefix = cwd.replace(/\/+$/, "") + "/";
    return cwd !== "" && line.startsWith(prefix)
      ? line.slice(prefix.length)
      : line;
  }
  return "";
}

/**
 * AI 此刻正在执行的动作：这一轮里最后一个开始执行、还没回结果的外层工具
 * （`pendingTools`，与计时器同一份口径），形如 `Edit · src/a.ts`。没有进行中的
 * 工具（在想、在写正文、或没有轮次在跑）时为 null。
 */
export function currentToolAction(
  stream: Pick<LiveStream, "pendingTools" | "liveBlocks"> | null,
  cwd: string,
): string | null {
  const id = stream?.pendingTools.at(-1);
  if (!stream || !id) return null;
  let block: (typeof stream.liveBlocks)[number] | undefined;
  for (let i = stream.liveBlocks.length - 1; i >= 0 && !block; i--) {
    const b = stream.liveBlocks[i];
    if (b.type === "tool_use" && b.toolUseId === id) block = b;
  }
  if (!block?.toolName) return null;
  const target = toolTarget(block.toolInput, cwd);
  return target === "" ? block.toolName : `${block.toolName} · ${target}`;
}

type Props = {
  messages: readonly Pick<chat_svc.ChatMessage, "role" | "blocks">[];
  /** 会话的主流（不含后台 subagent 活动流）；没有轮次在跑时为 null。 */
  stream: Pick<LiveStream, "pendingTools" | "liveBlocks"> | null;
  cwd: string;
};

/**
 * 文件占满时对话窄栏的上半部分（spec「文件占满」）：最后一条用户消息、AI 正在执行
 * 的动作（没有就不画这一行）。输入框是对话内容页原来那一个，不在这里。
 */
export function ChatRailSummary({ messages, stream, cwd }: Props) {
  const { t } = useTranslation();
  const lastAsk = lastUserMessageText(messages);
  const action = currentToolAction(stream, cwd);

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-auto px-3 pt-3">
      <h2 className="flex items-center gap-1.5 text-xs font-semibold text-foreground">
        <MessageSquare className="size-3.5" aria-hidden="true" />
        {t("editorSplit.rail.title")}
      </h2>
      {lastAsk === null ? null : (
        <p className="line-clamp-6 rounded-lg bg-muted px-2.5 py-2 text-xs break-words whitespace-pre-wrap text-foreground">
          {lastAsk}
        </p>
      )}
      {action === null ? null : (
        <div
          data-testid="chat-rail-action"
          className="flex min-w-0 items-start gap-1.5 text-xs text-muted-foreground"
        >
          <StatusDot status="running" size="xs" className="mt-1.5" />
          <span className="min-w-0 truncate font-mono">{action}</span>
        </div>
      )}
    </div>
  );
}
