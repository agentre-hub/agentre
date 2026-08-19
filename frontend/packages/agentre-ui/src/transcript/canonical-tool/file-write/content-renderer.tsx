import * as React from "react";
import { Copy } from "lucide-react";

import { Button } from "../../../ui/button";
import { copyTextWithToast } from "../../../lib/clipboard-toast";
import { useUiTranslation } from "../../../i18n";

import type { FileWriteDTO } from "../types";

// content-renderer.tsx —— canonical.file.write 的内容正文渲染器,与
// file-edit/hunk-renderer.tsx 同一角色:把「一次写入长什么样」从卡片外壳里抽出
// 来,好让整卡与活动块的行内展开体共用同一份渲染(spec 决策 13:展开体按
// canonical.kind 复用既有卡片正文,不因为改成活动行就退化成裸文本)。
//
// 带行号 + 横向滚动 + 截断条(含「复制完整内容」)三件都属于正文:后端在
// WriteContentByteCap 处截断并标 truncated,少了截断条用户不知道自己读到的是
// 被砍过的内容,也拿不到完整原文。

export function FileWriteContent({
  write,
}: {
  write: FileWriteDTO;
}): React.ReactElement {
  const { t } = useUiTranslation();
  if (write.content === "") {
    return (
      <div className="px-3 py-1 text-muted-foreground">
        {t("canonical.fileWrite.empty")}
      </div>
    );
  }
  return (
    <>
      {/* 内容行不换行(whitespace-pre),超宽的行必须能横向滚动 —— 卡片本身
          是 overflow-hidden,少了这层滚动容器长行就被直接裁掉且拖不出来。
          行盒 w-max + min-w-full,滚动后整行(含行号列对齐)才不会断在卡片边缘。 */}
      <div data-testid="file-write-content-scroll" className="overflow-x-auto">
        {write.content.split("\n").map((text, i) => (
          <div
            key={i}
            className="flex w-max min-w-full items-center px-3 py-0.5"
          >
            <span className="w-8 shrink-0 text-right text-meta text-decorative-foreground">
              {i + 1}
            </span>
            <span
              className="w-5 shrink-0 text-center text-meta text-decorative-foreground"
              aria-hidden="true"
            >
              {" "}
            </span>
            <span className="whitespace-pre text-foreground">{text}</span>
          </div>
        ))}
      </div>
      {write.truncated && (
        <TruncatedBar content={write.content} lines={write.lines} />
      )}
    </>
  );
}

function TruncatedBar({ content, lines }: { content: string; lines: number }) {
  const { t } = useUiTranslation();
  const [copyState, setCopyState] = React.useState<
    "copied" | "failed" | "idle"
  >("idle");
  const resetTimerRef = React.useRef<number | null>(null);

  React.useEffect(() => {
    return () => {
      if (resetTimerRef.current !== null) {
        window.clearTimeout(resetTimerRef.current);
      }
    };
  }, []);

  async function handleCopy() {
    if (resetTimerRef.current !== null) {
      window.clearTimeout(resetTimerRef.current);
      resetTimerRef.current = null;
    }
    try {
      const copied = await copyTextWithToast(content, {
        errorTitle: t("canonical.fileWrite.copyFailed"),
        successTitle: t("canonical.fileWrite.copyDone"),
      });
      setCopyState(copied ? "copied" : "failed");
    } catch {
      setCopyState("failed");
    }
    resetTimerRef.current = window.setTimeout(() => {
      setCopyState("idle");
      resetTimerRef.current = null;
    }, 1400);
  }

  return (
    <div className="mt-1 flex items-center gap-2 border-t border-border px-3 py-1">
      <span className="text-meta text-muted-foreground">
        {t("canonical.fileWrite.truncated", { lines })}
      </span>
      <span className="ml-auto" />
      <Button
        type="button"
        variant="ghost"
        size="xs"
        className="h-5 gap-1 px-1.5 text-meta text-muted-foreground"
        onClick={() => void handleCopy()}
      >
        <Copy data-icon="inline-start" aria-hidden="true" />
        {copyState === "copied"
          ? t("common.copied")
          : copyState === "failed"
            ? t("common.copyFailed")
            : t("canonical.fileWrite.copyFull")}
      </Button>
    </div>
  );
}
