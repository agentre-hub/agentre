import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Button } from "./button";

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
