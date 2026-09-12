import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { AgentredServiceSection } from "./agentred-service-section";

function renderSection(
  over: Partial<React.ComponentProps<typeof AgentredServiceSection>> = {},
) {
  const view = render(<AgentredServiceSection method="native" {...over} />);
  return view;
}

const bodyText = () =>
  screen
    .getAllByRole("group")
    .map((card) => card.textContent ?? "")
    .join("\n");

/**
 * 「让它常驻」两端做的是同一件事，只是控制台没有前台那一档：它的设备要长期在线，
 * 给一个会随终端关闭而消失的选项没有意义。差异用**端口的有无**表达 —— 宿主不给
 * `onRunModeChange`，选择器就不存在，而不是在共享组件里判断自己跑在哪个宿主。
 */
describe("AgentredServiceSection", () => {
  it("原生后台：给注册启动、查看状态、重启三条", () => {
    renderSection({ runMode: "service", onRunModeChange: vi.fn() });

    const text = bodyText();
    expect(text).toContain("agentred service install --start");
    expect(text).toContain("agentred service status");
    expect(text).toContain("agentred service restart");
  });

  it("原生前台：只给 agentred run", () => {
    renderSection({ runMode: "foreground", onRunModeChange: vi.fn() });

    const text = bodyText();
    expect(text).toContain("agentred run");
    expect(text).not.toContain("service install");
  });

  it("切换运行方式交回宿主", () => {
    const onRunModeChange = vi.fn();
    renderSection({ runMode: "service", onRunModeChange });

    fireEvent.click(
      screen.getByRole("button", { name: /temporary foreground/i }),
    );

    expect(onRunModeChange).toHaveBeenCalledWith("foreground");
  });

  it("宿主不给 onRunModeChange 就没有运行方式选择器，只剩后台那一条路", () => {
    renderSection();

    expect(
      screen.queryByRole("button", { name: /temporary foreground/i }),
    ).toBeNull();
    expect(bodyText()).toContain("agentred service install --start");
  });

  it("Docker 方式换成 compose 的起容器、看状态、跟日志", () => {
    renderSection({ method: "docker" });

    const text = bodyText();
    expect(text).toContain("docker compose up -d");
    expect(text).toContain("docker compose ps");
    expect(text).toContain("docker compose logs -f agentred");
    expect(text).not.toContain("agentred service install");
  });

  /** 容器方式没有「前台临时」这回事——compose 起的就是后台。 */
  it("Docker 方式即便宿主给了端口也不画运行方式选择器", () => {
    renderSection({
      method: "docker",
      runMode: "service",
      onRunModeChange: vi.fn(),
    });

    expect(
      screen.queryByRole("button", { name: /temporary foreground/i }),
    ).toBeNull();
  });
});
