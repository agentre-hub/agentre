import { render, screen } from "@testing-library/react";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

import { Button } from "./button";
import { TouchProvider } from "./touch";

// outline 与 secondary 是同一套外观：按钮填充（静止 8%、悬停 12%），没有边。
// 填充对脚下各表面的可分度、弱化文字对填充的对比度由 tokens.test.ts 守着。

describe("Button · outline 与 secondary 同一套填充", () => {
  const fillClasses = (name: string) =>
    Array.from(screen.getByRole("button", { name }).classList).filter((c) =>
      /(?:^|:)bg-/.test(c),
    );

  it("outline 用按钮填充（静止 8%、悬停 12%），没有边", () => {
    render(<Button variant="outline">取消</Button>);
    const classes = Array.from(
      screen.getByRole("button", { name: "取消" }).classList,
    );

    expect(classes).toContain("bg-button-fill");
    expect(classes).toContain("hover:bg-button-fill-hover");
    expect(classes.filter((c) => /^(?:dark:)?border(?:-|$)/.test(c))).toEqual(
      [],
    );
  });

  it("outline 与 secondary 的底色类完全一致", () => {
    render(
      <>
        <Button variant="outline">取消</Button>
        <Button variant="secondary">测试连接</Button>
      </>,
    );

    expect(fillClasses("取消")).toEqual(fillClasses("测试连接"));
    expect(fillClasses("取消")).toContain("bg-button-fill");
  });
});

describe("Button · 触屏按下态", () => {
  const pressed = (name: string) =>
    Array.from(screen.getByRole("button", { name }).classList).filter((c) =>
      c.startsWith("active:"),
    );

  it("touch：每个变体按下时底色加深一档", () => {
    for (const variant of [
      "default",
      "destructive",
      "outline",
      "secondary",
      "ghost",
    ] as const) {
      const { unmount } = render(
        <Button variant={variant} touch>
          {variant}
        </Button>,
      );
      expect(pressed(variant).length, variant).toBeGreaterThan(0);
      unmount();
    }
  });

  it("包在 TouchProvider 里等同 touch", () => {
    render(
      <TouchProvider>
        <Button variant="outline">取消</Button>
      </TouchProvider>,
    );
    expect(pressed("取消").length).toBeGreaterThan(0);
  });

  it("不传 touch：桌面没有按下态类，touch 也不漏到 DOM 属性", () => {
    render(<Button variant="outline">取消</Button>);
    expect(pressed("取消")).toEqual([]);
    expect(screen.getByRole("button", { name: "取消" })).not.toHaveAttribute(
      "touch",
    );
  });
});

describe("Button · 触屏按下态真的更深（I2）", () => {
  // 实底按钮按下不能用「同色降不透明度」：bg-primary 本身不透明，/80 叠在浅色底上
  // 反而更浅。要换到一个比静止色更深的 token。
  it("主按钮与危险按钮按下换到 *-pressed token，而不是降不透明度", () => {
    for (const [variant, token] of [
      ["default", "active:bg-primary-pressed"],
      ["destructive", "active:bg-destructive-pressed"],
    ] as const) {
      const { unmount } = render(
        <Button variant={variant} touch>
          {variant}
        </Button>,
      );
      const classes = Array.from(
        screen.getByRole("button", { name: variant }).classList,
      );
      expect(classes, variant).toContain(token);
      expect(
        classes.filter((c) => /^active:bg-(primary|destructive)\/\d+$/.test(c)),
        variant,
      ).toEqual([]);
      unmount();
    }
  });

  it("*-pressed token 在 tokens.css 里朝黑色混深，跟着两套主题走", () => {
    // 包内跑与在 frontend/ 下跑，工作目录不同。
    const path = [
      resolve(process.cwd(), "packages/agentre-ui/styles/tokens.css"),
      resolve(process.cwd(), "styles/tokens.css"),
    ].find((p) => existsSync(p));
    const css = readFileSync(path!, "utf8");
    for (const name of ["primary", "destructive"]) {
      expect(css).toMatch(
        new RegExp(
          `--color-${name}-pressed:\\s*color-mix\\(\\s*in oklab,\\s*var\\(--${name}\\)\\s*\\d+%,\\s*black\\s*\\)`,
        ),
      );
    }
  });
});
