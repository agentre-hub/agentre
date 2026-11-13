import { render, screen } from "@testing-library/react";
import i18n from "i18next";
import { afterEach, describe, expect, it } from "vitest";

import { StatusMark } from "./status-mark";
import type { StatusMarkState } from "./status-mark";

afterEach(async () => {
  if (i18n.language !== "en") await i18n.changeLanguage("en");
});

/**
 * 「状态 → 图标」对照表（spec 2026-11-13-running-status-icon 决策 1–4、
 * docs/design.md §6.4）。图标按 lucide 的类名认，颜色按 design.md §3.5 的
 * 角色认：独立图标用饱和的 `status-*`，修改用 `primary-text`，其余终态 muted。
 */
const TABLE: ReadonlyArray<{
  state: StatusMarkState;
  icon: string;
  tone: string;
  zh: string;
  en: string;
}> = [
  {
    state: "running",
    icon: "lucide-loader-circle",
    tone: "text-status-running",
    zh: "运行中",
    en: "Running",
  },
  {
    state: "done",
    icon: "lucide-check",
    tone: "text-status-running",
    zh: "已完成",
    en: "Done",
  },
  {
    state: "failed",
    icon: "lucide-triangle-alert",
    tone: "text-status-error",
    zh: "失败",
    en: "Failed",
  },
  {
    state: "error",
    icon: "lucide-triangle-alert",
    tone: "text-status-error",
    zh: "出错",
    en: "Error",
  },
  {
    state: "stopped",
    icon: "lucide-circle-slash",
    tone: "text-muted-foreground",
    zh: "已停止",
    en: "Stopped",
  },
  {
    state: "cancelled",
    icon: "lucide-circle-slash",
    tone: "text-muted-foreground",
    zh: "已取消",
    en: "Cancelled",
  },
  {
    state: "unknown",
    icon: "lucide-circle-help",
    tone: "text-muted-foreground",
    zh: "结果未知",
    en: "Result unknown",
  },
  {
    state: "skipped",
    icon: "lucide-skip-forward",
    tone: "text-muted-foreground",
    zh: "已跳过",
    en: "Skipped",
  },
  {
    state: "expired",
    icon: "lucide-hourglass",
    tone: "text-muted-foreground",
    zh: "已过期",
    en: "Expired",
  },
  {
    state: "invalidated",
    icon: "lucide-hourglass",
    tone: "text-muted-foreground",
    zh: "已失效",
    en: "No longer valid",
  },
  {
    state: "queued",
    icon: "lucide-circle",
    tone: "text-muted-foreground",
    zh: "排队中",
    en: "Queued",
  },
  {
    state: "answered",
    icon: "lucide-check",
    tone: "text-status-running",
    zh: "已回答",
    en: "Answered",
  },
  {
    state: "approved",
    icon: "lucide-check",
    tone: "text-status-running",
    zh: "已批准",
    en: "Approved",
  },
  {
    state: "allowed",
    icon: "lucide-shield-check",
    tone: "text-status-running",
    zh: "已允许",
    en: "Allowed",
  },
  {
    state: "denied",
    icon: "lucide-x",
    tone: "text-status-error",
    zh: "已拒绝",
    en: "Denied",
  },
  {
    state: "created",
    icon: "lucide-plus",
    tone: "text-status-running",
    zh: "新建",
    en: "Created",
  },
  {
    state: "updated",
    icon: "lucide-pencil",
    tone: "text-primary-text",
    zh: "修改",
    en: "Updated",
  },
  {
    state: "deleted",
    icon: "lucide-trash2",
    tone: "text-status-error",
    zh: "删除",
    en: "Deleted",
  },
  {
    state: "fileNew",
    icon: "lucide-file-plus",
    tone: "text-status-running",
    zh: "新文件",
    en: "New file",
  },
  {
    state: "fileDeleted",
    icon: "lucide-file-x",
    tone: "text-status-error",
    zh: "已删除",
    en: "Deleted",
  },
  {
    state: "memoryCreated",
    icon: "lucide-bookmark-plus",
    tone: "text-status-running",
    zh: "记住了",
    en: "Remembered",
  },
  {
    state: "memoryUpdated",
    icon: "lucide-pencil",
    tone: "text-primary-text",
    zh: "改了记忆",
    en: "Updated a memory",
  },
  {
    state: "memoryArchived",
    icon: "lucide-archive",
    tone: "text-muted-foreground",
    zh: "归档了记忆",
    en: "Archived a memory",
  },
  {
    state: "memoryExists",
    icon: "lucide-bookmark-check",
    tone: "text-muted-foreground",
    zh: "已有这条记忆",
    en: "Already remembered",
  },
  {
    state: "memoryUndone",
    icon: "lucide-undo2",
    tone: "text-muted-foreground",
    zh: "已撤销",
    en: "Undone",
  },
];

function iconOf(mark: HTMLElement): SVGElement {
  const svg = mark.querySelector("svg");
  if (!svg) throw new Error("expected StatusMark to draw an icon");
  return svg;
}

describe("StatusMark", () => {
  describe.each(TABLE)(
    "Given state $state",
    ({ state, icon, tone, zh, en }) => {
      it("When rendered in English, Then it draws its own icon in its colour role, named and tipped in English", () => {
        render(<StatusMark state={state} />);

        const mark = screen.getByRole("img", { name: en });
        expect(mark).toHaveAttribute("title", en);
        expect(mark).toHaveClass(tone);
        expect(iconOf(mark)).toHaveClass(icon);
        // 名字已在外层；图标本身不再被读一遍。
        expect(iconOf(mark)).toHaveAttribute("aria-hidden", "true");
      });

      it("When the UI language is zh-CN, Then the accessible name and hover tip are Chinese", async () => {
        await i18n.changeLanguage("zh-CN");
        render(<StatusMark state={state} />);

        const mark = screen.getByRole("img", { name: zh });
        expect(mark).toHaveAttribute("title", zh);
      });
    },
  );

  it("Given running, When rendered, Then the spinner spins but stops under reduced motion", () => {
    render(<StatusMark state="running" />);

    const svg = iconOf(screen.getByRole("img", { name: "Running" }));
    expect(svg).toHaveClass("animate-spin", "motion-reduce:animate-none");
  });

  it("Given a terminal state, When rendered, Then nothing spins", () => {
    render(<StatusMark state="stopped" />);

    const svg = iconOf(screen.getByRole("img", { name: "Stopped" }));
    expect(svg).not.toHaveClass("animate-spin");
  });

  it("Given a caller className, When rendered, Then it is merged onto the mark and can resize it", () => {
    render(<StatusMark state="failed" className="size-4 ml-1" />);

    const mark = screen.getByRole("img", { name: "Failed" });
    expect(mark).toHaveClass("size-4", "ml-1", "text-status-error");
    expect(mark).not.toHaveClass("size-3.5");
    expect(mark).toHaveAttribute("data-state", "failed");
  });
});
