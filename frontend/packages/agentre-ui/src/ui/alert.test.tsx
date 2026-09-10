import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Alert, AlertDescription, AlertTitle } from "./alert";

/**
 * 合并后的唯一一份 Alert。
 *
 * 三份副本只差一档：agentre-server 那份多一个 `warning`，桌面端与包内没有。
 * 合并保留它 —— 桌面端多一档暂时没人用的 variant，代价远小于让 agentre-server
 * 继续维持一份副本（副本正是靠「就差这一点点」重新长出来的）。
 */
describe("Alert", () => {
  it("默认档是中性的卡片面", () => {
    render(
      <Alert>
        <AlertTitle>已保存</AlertTitle>
      </Alert>,
    );

    expect(screen.getByRole("alert")).toHaveClass("text-card-foreground");
  });

  it("destructive 档用语义 token 而不是红色字面量", () => {
    render(
      <Alert variant="destructive">
        <AlertDescription>连接失败</AlertDescription>
      </Alert>,
    );

    const alert = screen.getByRole("alert");

    expect(alert).toHaveClass("text-destructive");
    expect(alert.className).not.toMatch(/text-red-|bg-red-/);
  });

  it("warning 档在，取色是 status-waiting", () => {
    // 「需要你处理，但还不是故障」这一档：agentre-server 的设备与会话状态在用。
    // 没有它，那些地方只能在「中性」和「红」之间二选一，红色因此变廉价。
    render(
      <Alert variant="warning">
        <AlertDescription>设备已离线</AlertDescription>
      </Alert>,
    );

    expect(screen.getByRole("alert")).toHaveClass("text-status-waiting");
  });
});
