import * as React from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { classifyLink } from "@agentre-hub/agentre-ui";

import { OpenPath, RevealPath } from "@/../wailsjs/go/app/App";

/**
 * openTarget 把面板里的一条路径解析成能交给系统打开的绝对路径：工具调用给的
 * 路径可能已经是绝对路径（「变动」模式），也可能是相对会话工作目录的路径
 * （「目录」模式），两者都由 classifyLink 统一判定，调用方不再各自拼一次。
 */
export function openTarget(path: string, cwd: string): string {
  const link = classifyLink(path, cwd);
  if (link.kind === "local-internal" || link.kind === "local-external") {
    return link.fullPath;
  }
  return `${cwd}/${path}`;
}

/**
 * useOpenFile 返回「用系统默认应用打开这条路径」的回调。文件面板的各个模式共用
 * 同一份打开语义与同一条失败提示。调用方自己决定按钮是否渲染（仅本地会话且 cwd
 * 非空）。
 */
export function useOpenFile(cwd: string): (path: string) => void {
  const { t } = useTranslation();
  return React.useCallback(
    (path: string) => {
      OpenPath(openTarget(path, cwd)).catch((err: unknown) => {
        toast.error(
          t("chatContext.files.openFailed", {
            error: errorMessage(err),
          }),
        );
      });
    },
    [cwd, t],
  );
}

/**
 * useRevealFile 返回「在系统文件管理器中显示这条路径」的回调，路径解析与失败
 * 提示都与 useOpenFile 同源；两者都只在本地会话被调用（远端会话下菜单项整项
 * 不渲染，spec「右键菜单」）。
 */
export function useRevealFile(cwd: string): (path: string) => void {
  const { t } = useTranslation();
  return React.useCallback(
    (path: string) => {
      RevealPath(openTarget(path, cwd)).catch((err: unknown) => {
        toast.error(
          t("chatContext.row.revealFailed", { error: errorMessage(err) }),
        );
      });
    },
    [cwd, t],
  );
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
