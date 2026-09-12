import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { CommandCard } from "./command-card";

const originalClipboard = navigator.clipboard;
const originalExecCommand = document.execCommand;

function setClipboard(value: unknown) {
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value,
  });
}

afterEach(() => {
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: originalClipboard,
  });
  Object.defineProperty(document, "execCommand", {
    configurable: true,
    value: originalExecCommand,
  });
});

/**
 * 反馈走按钮内联的「已复制」，不走 toast。
 *
 * 引导这一页最常被部署在 `http://<局域网 IP>:port` 上，那里 `navigator.clipboard`
 * 整个对象都不存在（规范标了 [SecureContext]），只剩 `execCommand` 兜底；兜底也
 * 失败时按钮**必须保持原样**，让人知道要手抄。toast 报的是「动作发生了」，
 * 表达不了这件事，还要求宿主装着同一个 toast 实例。
 */
describe("CommandCard", () => {
  it("命令原样交给剪贴板，按钮当场翻成已复制", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    setClipboard({ writeText });

    render(<CommandCard label="Remote terminal" command="agentred pair" />);
    fireEvent.click(screen.getByRole("button", { name: /copy/i }));

    expect(writeText).toHaveBeenCalledWith("agentred pair");
    expect(await screen.findByText("Copied")).toBeTruthy();
  });

  /**
   * 记的是「复制走的是哪一条命令」而非一个布尔值：切换系统会把同一张卡换成另一条
   * 命令，只记布尔的话按钮就会对着一条从没进过剪贴板的命令说「已复制」。
   */
  it("换成另一条命令后撤掉已复制，剪贴板里躺着的还是上一条", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    setClipboard({ writeText });

    const view = render(
      <CommandCard label="Remote terminal" command="agentred pair" />,
    );
    fireEvent.click(screen.getByRole("button", { name: /copy/i }));
    expect(await screen.findByText("Copied")).toBeTruthy();

    view.rerender(
      <CommandCard label="Remote terminal" command="agentred run" />,
    );

    expect(screen.getByRole("group")).toHaveTextContent("agentred run");
    expect(writeText).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("Copied")).toBeNull();
  });

  it("非安全上下文没有 Clipboard API：按钮照样在，退回 execCommand 也能复制", async () => {
    setClipboard(undefined);
    Object.defineProperty(document, "execCommand", {
      configurable: true,
      value: vi.fn().mockReturnValue(true),
    });

    render(<CommandCard label="Remote terminal" command="agentred pair" />);
    fireEvent.click(screen.getByRole("button", { name: /copy/i }));

    expect(await screen.findByText("Copied")).toBeTruthy();
  });

  it("execCommand 也复制不成：不谎报已复制，命令仍留在页面上可手抄", async () => {
    setClipboard(undefined);
    const execCommand = vi.fn().mockReturnValue(false);
    Object.defineProperty(document, "execCommand", {
      configurable: true,
      value: execCommand,
    });

    render(<CommandCard label="Remote terminal" command="agentred pair" />);
    fireEvent.click(screen.getByRole("button", { name: /copy/i }));

    // 先等这次复制真的尝试过并把结果送回组件 —— 少了这一步，下面那句
    // queryByText 只是在断言「这一拍还没重渲染」，谎报「已复制」也照样绿。
    await waitFor(() => expect(execCommand).toHaveBeenCalledWith("copy"));
    expect(screen.queryByText("Copied")).toBeNull();
    expect(screen.getByRole("group")).toHaveTextContent("agentred pair");
  });

  /** 宿主的用例按 testid 抓卡片与复制按钮，所以这两个钩子要透传下去。 */
  it("宿主给的 testId 落到命令与复制按钮上", () => {
    render(
      <CommandCard
        label="Remote terminal"
        command="agentred pair"
        testId="add-device-command-install"
        copyTestId="add-device-copy-install"
      />,
    );

    expect(screen.getByTestId("add-device-command-install")).toHaveTextContent(
      "agentred pair",
    );
    expect(screen.getByTestId("add-device-copy-install")).toBeTruthy();
  });
});
