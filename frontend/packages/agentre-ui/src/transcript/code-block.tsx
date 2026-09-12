import * as React from "react";
import { Copy } from "lucide-react";
import { useUiTranslation } from "../i18n";

import { Button } from "../ui/button";
import { copyTextWithToast } from "../lib/clipboard-toast";

import { TranscriptCard } from "./transcript-card";

export type CodeBlockProps = React.ComponentProps<"section"> & {
  children: React.ReactNode;
  language?: string;
};

function extractTextFromReactNode(node: React.ReactNode): string {
  if (node === null || node === undefined || typeof node === "boolean") {
    return "";
  }
  if (typeof node === "string" || typeof node === "number") {
    return String(node);
  }
  if (Array.isArray(node)) {
    return node.map(extractTextFromReactNode).join("");
  }
  if (React.isValidElement<{ children?: React.ReactNode }>(node)) {
    return extractTextFromReactNode(node.props.children);
  }
  return "";
}

export function CodeBlock({
  children,
  className,
  language = "preview",
  ...props
}: CodeBlockProps) {
  const { t } = useUiTranslation();
  const [copyState, setCopyState] = React.useState<
    "copied" | "failed" | "idle"
  >("idle");
  const resetTimerRef = React.useRef<number | null>(null);
  const codeText = React.useMemo(
    () => extractTextFromReactNode(children),
    [children],
  );

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
      const copied = await copyTextWithToast(codeText, {
        errorTitle: t("codeBlock.copyFailed"),
        successTitle: t("codeBlock.copyDone"),
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
    <TranscriptCard className={className} {...props}>
      <div className="flex items-center gap-2 border-b border-border px-3.5 py-2">
        <span className="text-meta font-semibold text-muted-foreground">
          {language}
        </span>
        <span className="min-w-0 flex-1" />
        <Button
          type="button"
          variant="ghost"
          size="xs"
          className="h-6 gap-1 px-1.5 text-meta text-muted-foreground"
          onClick={() => void handleCopy()}
        >
          <Copy data-icon="inline-start" aria-hidden="true" />
          {copyState === "copied"
            ? t("common.copied")
            : copyState === "failed"
              ? t("common.copyFailed")
              : t("common.copy")}
        </Button>
      </div>
      <pre
        data-selectable-text="true"
        className="overflow-auto px-3.5 py-3 font-mono text-aux text-foreground"
      >
        {children}
      </pre>
    </TranscriptCard>
  );
}
