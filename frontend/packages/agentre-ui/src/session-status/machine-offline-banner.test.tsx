import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { MachineOfflineBanner } from "./machine-offline-banner";
import { StatusBanner } from "./status-banner";

/**
 * 「这条对话钉住的机器离线了」——两端唯一都成立、且都已经各画过一份的那一档。
 *
 * 合并之前两端说的不是同一件事：
 *   - 桌面端 `SessionOfflineBanner`：「{{name}} 当前离线 / 上下文都在那台机器上，
 *     不会改派。等它上线，或用同样的开头新建一个会话。」+ 一个「新建一个会话」
 *   - agentre-server `SessionStatusBanner` 的 machineOffline 档：「{{machine}} 离线 /
 *     历史照常读。消息不排队——等它回来再发。」+「最后在线 X」+ 一个「查看设备」
 *
 * 两句都不错，强调的东西完全不同：一句讲「为什么不会自动换机器」，一句讲「历史还
 * 读得到、消息不会排队」。同一个用户在两端遇到同一件事，得到两种解释——这才是这
 * 一轮要消掉的东西。文案因此取并集，并住进包里（宿主不再各留一份）。
 *
 * 出口统一成「新建一个会话」：两端都有「去看设备」与「新建对话」两种能力（桌面端
 * 有 settings:remote-devices，web 有 chat.startNew），所以差异不是能力差异。而
 * 「查看设备」不把人往前推——横幅刚说完「离线 · 最后在线 3小时前」，点进去看到的
 * 还是那句话。**动作本身仍由宿主注入**：两端路由不同，包不认识任何一个。
 */
describe("MachineOfflineBanner", () => {
  it("Given 认得出机器名, When 渲染, Then 标题里说出是哪一台", () => {
    render(
      <MachineOfflineBanner
        machineName="mac-studio-01"
        onStartNew={() => {}}
      />,
    );
    expect(screen.getByText("mac-studio-01 is offline")).toBeTruthy();
  });

  it("Given 认不出机器名, When 渲染, Then 退到通用说法而不是编一个占位名", () => {
    render(<MachineOfflineBanner onStartNew={() => {}} />);
    expect(screen.getByText("This machine is offline")).toBeTruthy();
  });

  it("Given 渲染, When 读正文, Then 两端此前各说一半的话都在", () => {
    // 并集：server 那半（历史照常读 / 消息不排队）与桌面端那半（上下文在那台
    // 机器上、不会改派 / 等它上线或用同样的开头新建）。少哪一半都是回归。
    render(<MachineOfflineBanner machineName="m1" onStartNew={() => {}} />);
    const body = screen.getByTestId("status-banner-body").textContent ?? "";
    expect(body).toContain("History still reads");
    expect(body).toContain("not queued");
    expect(body).toContain("will not be reassigned");
    expect(body).toContain("start a new conversation");
  });

  it("Given 宿主给了「最后在线」, When 渲染, Then 接在正文后面", () => {
    render(
      <MachineOfflineBanner
        machineName="m1"
        lastSeen={{ text: "3 hours ago" }}
        onStartNew={() => {}}
      />,
    );
    expect(screen.getByText(/Last seen 3 hours ago/)).toBeTruthy();
  });

  it("Given 相对时间, When 宿主也给了精确时刻, Then 它挂在 <time> 上备查", () => {
    // 「3 小时前」读起来快，但要对齐日志时得有准确值。相对文字是可见的那一层，
    // 精确时刻挂在 title / dateTime 上——两者都由宿主格式化，包不认识时区与口径。
    render(
      <MachineOfflineBanner
        machineName="m1"
        lastSeen={{
          text: "3 hours ago",
          dateTime: "2026-08-21T06:32:07.000Z",
          exact: "2026/8/21 14:32:07",
        }}
        onStartNew={() => {}}
      />,
    );
    const time = screen.getByTestId("status-banner-last-seen");
    expect(time.tagName).toBe("TIME");
    expect(time.getAttribute("dateTime")).toBe("2026-08-21T06:32:07.000Z");
    expect(time.getAttribute("title")).toBe("2026/8/21 14:32:07");
  });

  it("Given 只给了相对文字, When 渲染, Then 不编一个 dateTime", () => {
    render(
      <MachineOfflineBanner
        machineName="m1"
        lastSeen={{ text: "3 hours ago" }}
        onStartNew={() => {}}
      />,
    );
    const time = screen.getByTestId("status-banner-last-seen");
    expect(time.hasAttribute("dateTime")).toBe(false);
    expect(time.hasAttribute("title")).toBe(false);
  });

  it("Given 宿主没给「最后在线」, When 渲染, Then 不编一个时刻", () => {
    render(<MachineOfflineBanner machineName="m1" onStartNew={() => {}} />);
    expect(screen.queryByText(/Last seen/)).toBeNull();
  });

  it("Given 渲染, When 看出口, Then 只有「新建一个会话」这一个，且文案与样式来自包", () => {
    // 出口是统一的那一档，因此按钮本身（文案 + 形态）住在包里；宿主只给「按下去
    // 往哪走」。两端各画一颗自己的按钮的话，同一个出口会长出两种字和两种尺寸。
    render(<MachineOfflineBanner machineName="m1" onStartNew={() => {}} />);
    const slot = screen.getByTestId("status-banner-action");
    expect(
      within(slot).getByRole("button", { name: "Start a new conversation" }),
    ).toBeTruthy();
  });

  it("Given 按下出口, When 触发, Then 调的是宿主给的那个回调", async () => {
    // 「往哪走」两端不同：桌面端就地开一条新会话，web 要回到它自己的新建对话流。
    // 包不认识任何一端的路由，所以只回调，不导航。
    const onStartNew = vi.fn();
    render(<MachineOfflineBanner machineName="m1" onStartNew={onStartNew} />);
    await userEvent.click(
      screen.getByRole("button", { name: "Start a new conversation" }),
    );
    expect(onStartNew).toHaveBeenCalledTimes(1);
  });

  it("Given 这一档, When 渲染, Then tone 是 alarm", () => {
    // 「够不着」是要去处理的，不是等它自己好——与 limited（读得了写不了）分开。
    render(<MachineOfflineBanner machineName="m1" onStartNew={() => {}} />);
    expect(screen.getByRole("alert").dataset.tone).toBe("alarm");
  });
});

describe("StatusBanner 外壳", () => {
  it("Given 三档 tone, When 渲染, Then 各自标记得出来", () => {
    for (const tone of ["alarm", "limited", "settled"] as const) {
      const { unmount } = render(
        <StatusBanner tone={tone} title="t" body="b" />,
      );
      expect(screen.getByRole("alert").dataset.tone).toBe(tone);
      unmount();
    }
  });

  it("Given 没有动作, When 渲染, Then 动作槽整个不出", () => {
    // 空槽会在窄容器下占一行高度，读者看到一块说不出用途的留白。
    render(<StatusBanner tone="settled" title="t" body="b" />);
    expect(screen.queryByTestId("status-banner-action")).toBeNull();
  });

  it("Given 宿主要给这一格做记号, When 透传 data-*, Then 它落在 alert 那一层", () => {
    // 宿主自己的状态机比包知道的多（agentre-server 有九个视图状态、三档 tier），
    // 它要能在 DOM 上说出「这一张现在说的是哪一档」——给测试、给截图脚本、给
    // 将来的样式钩子。包不认识那些名字，所以不解释，只透传。
    render(
      <StatusBanner
        tone="alarm"
        title="t"
        body="b"
        data-session-status="machineOffline"
        data-tier="blocking"
      />,
    );
    const root = screen.getByRole("alert");
    expect(root.getAttribute("data-session-status")).toBe("machineOffline");
    expect(root.getAttribute("data-tier")).toBe("blocking");
    // 透传不该把 tone 顶掉。
    expect(root.dataset.tone).toBe("alarm");
  });

  it("Given sticky, When 渲染, Then 吸顶类在外层", () => {
    // server 的横幅要吸在转录滚动带顶部（往下读一屏，输入框为什么灰着的解释还在）；
    // 桌面端那张挂在聊天头下面、不滚动。所以是 prop 而不是写死。
    render(<StatusBanner tone="alarm" title="t" body="b" sticky />);
    expect(screen.getByRole("alert").className).toContain("sticky");
  });
});
