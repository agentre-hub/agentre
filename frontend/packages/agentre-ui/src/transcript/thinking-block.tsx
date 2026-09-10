import * as React from "react";
import { useUiTranslation } from "../i18n";
import { Brain, ChevronDown } from "lucide-react";
import { shouldIgnoreClickForSelection } from "../lib/copyable-text";

import { cn } from "../lib/utils";

import {
  TranscriptCard,
  TranscriptCardHeader,
  TranscriptPill,
} from "./transcript-card";
import { useTranscriptBooleanState } from "./transcript-ui-state";

// STREAMING_TAIL_MAX_CHARS 限制 streaming 视图实际渲染的思考文本长度。streaming body
// 本就是 max-h-[132px] overflow-hidden 并贴底显示 —— 头部永远看不见。若把整段 text 都
// 塞进 DOM,每个 chunk 都要 O(n) 重排 whitespace-pre-wrap 文本块,一整轮流式下来是
// O(n²),长思考流会卡死主线程(用户可见症状:app 卡死/卡顿)。只渲染末尾一个窗口量级
// 的尾巴,单 chunk 渲染降到 O(1)。完整 text 仍由 store 累积,done/落库态照常使用全文。
const STREAMING_TAIL_MAX_CHARS = 1200;

// streamingTail 取 text 末尾不超过 STREAMING_TAIL_MAX_CHARS 的一段。cut 落在代理对
// 后半时回退一格,避免开头出现半个代理对被渲染成替换符。
function streamingTail(text: string): string {
  if (text.length <= STREAMING_TAIL_MAX_CHARS) return text;
  const cut = text.length - STREAMING_TAIL_MAX_CHARS;
  let start = cut;
  if (start > 0) {
    const code = text.charCodeAt(start);
    if (code >= 0xdc00 && code <= 0xdfff) start -= 1;
  }
  return text.slice(start);
}

type ThinkingBlockProps = {
  text: string;
  /** 该 block 是否正处在流式输出中。父组件根据 stream 上下文判断后传入。 */
  streaming: boolean;
  /**
   * 外部传入的计时起点 (Unix ms)。父组件已知更早的起点(例如 stream 真正开始的瞬间) 时传入,
   * 用于解决 Claude Code CLI 把整段 thinking 一次性发出来、合成块只活几 ms 自计时只能拿到 0s 的问题。
   * 不传时退化为「组件首次挂载时」自计时。
   */
  startedAt?: number;
  uiStateKey?: string;
};

export function ThinkingBlock({
  text,
  streaming,
  startedAt: externalStartedAt,
  uiStateKey,
}: ThinkingBlockProps) {
  const { t } = useUiTranslation();
  // streaming 期间默认展开,纯历史(streaming=false 渲染)默认折叠,完成时再强制收回(见下方 effect)。
  const [expanded, setExpanded] = useTranscriptBooleanState(
    uiStateKey,
    streaming,
  );
  // 自计时回退:仅当外部没传 startedAt 时使用。
  const [internalStartedAt, setInternalStartedAt] = React.useState<
    number | null
  >(null);
  const startedAt = externalStartedAt ?? internalStartedAt;
  const [liveSeconds, setLiveSeconds] = React.useState(0);
  const [finalSeconds, setFinalSeconds] = React.useState<number | null>(null);
  // streaming body 在内容超过 max-h 时手动把 scrollTop 推到底部，
  // 短内容下 scrollHeight === clientHeight，scrollTop 自然为 0，没有空白。
  const streamingBodyRef = React.useRef<HTMLDivElement>(null);
  React.useLayoutEffect(() => {
    if (!streaming) return;
    const el = streamingBodyRef.current;
    if (!el) return;
    el.scrollTop = el.scrollHeight;
  }, [streaming, text]);

  // streaming → done 转换瞬间强制收回展开态,避免用户在 streaming 中点开后完成态仍卡在展开。
  const prevStreamingRef = React.useRef(streaming);
  React.useEffect(() => {
    if (prevStreamingRef.current && !streaming) {
      setExpanded(false);
    }
    prevStreamingRef.current = streaming;
  }, [setExpanded, streaming]);

  React.useEffect(() => {
    if (!streaming) {
      // 流式结束:如果曾有起点 (外部传入或自记),把当前 (Date.now() - startedAt) 固化为 final。
      if (startedAt !== null && finalSeconds === null) {
        setFinalSeconds(Math.floor((Date.now() - startedAt) / 1000));
      }
      return undefined;
    }
    // 流式中:外部没传 startedAt 时首次进入记 internalStartedAt;然后每秒推进 liveSeconds。
    if (externalStartedAt === undefined && internalStartedAt === null) {
      setInternalStartedAt(Date.now());
    }
    const start = startedAt ?? Date.now();
    setLiveSeconds(Math.floor((Date.now() - start) / 1000));
    const id = setInterval(() => {
      setLiveSeconds(Math.floor((Date.now() - start) / 1000));
    }, 1000);
    return () => clearInterval(id);
  }, [
    streaming,
    startedAt,
    finalSeconds,
    externalStartedAt,
    internalStartedAt,
  ]);

  if (!text) return null;

  const charCount = text.length;

  let metaText = "";
  if (!streaming) {
    metaText =
      finalSeconds !== null
        ? t("thinking.meta.withSeconds", {
            seconds: finalSeconds,
            count: charCount,
          })
        : t("thinking.meta.charCount", { count: charCount });
  }

  const handleToggle = (event: React.MouseEvent<HTMLButtonElement>) => {
    if (shouldIgnoreClickForSelection(event)) return;
    setExpanded((v) => !v);
  };

  return (
    <TranscriptCard data-selectable-text="true">
      <TranscriptCardHeader
        onClick={handleToggle}
        aria-expanded={expanded}
        aria-label={
          streaming ? t("thinking.toggleStreaming") : t("thinking.toggleDone")
        }
      >
        <Brain
          aria-hidden
          className={cn(
            "size-4 shrink-0 text-primary",
            !streaming && "opacity-70",
          )}
        />
        <span
          data-copyable-control-text="true"
          className="text-sm font-medium text-foreground"
        >
          {streaming ? t("thinking.streaming") : t("thinking.done")}
        </span>
        {streaming ? (
          <TranscriptPill
            data-copyable-control-text="true"
            className="bg-primary-soft text-primary-text"
          >
            {liveSeconds}s
          </TranscriptPill>
        ) : metaText ? (
          <span
            data-copyable-control-text="true"
            className="text-meta text-muted-foreground"
          >
            {metaText}
          </span>
        ) : null}
        <span className="flex-1" />
        {streaming ? (
          <span
            aria-hidden
            className="size-1.5 shrink-0 rounded-full bg-primary motion-safe:animate-pulse"
          />
        ) : null}
        <ChevronDown
          aria-hidden
          className={cn(
            "size-4 shrink-0 text-muted-foreground transition-transform duration-150 ease-out motion-reduce:transition-none",
            expanded && "rotate-180",
          )}
        />
      </TranscriptCardHeader>
      <div
        data-slot="thinking-block-content"
        className="grid transition-[grid-template-rows] duration-200 ease-out motion-reduce:transition-none"
        style={{ gridTemplateRows: expanded ? "1fr" : "0fr" }}
        aria-hidden={!expanded}
      >
        <div className="min-h-0 overflow-hidden">
          <div className="border-t border-border">
            {streaming ? (
              <div
                ref={streamingBodyRef}
                data-slot="thinking-streaming-body"
                className="max-h-[132px] overflow-hidden whitespace-pre-wrap break-words px-3.5 py-3 text-aux italic text-muted-foreground"
              >
                {streamingTail(text)}
              </div>
            ) : (
              <div className="whitespace-pre-wrap break-words px-3.5 py-3 text-aux italic text-muted-foreground">
                {text}
              </div>
            )}
          </div>
        </div>
      </div>
    </TranscriptCard>
  );
}
