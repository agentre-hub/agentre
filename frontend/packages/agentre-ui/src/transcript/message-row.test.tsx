import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { MessageCopyButton, MessageRow } from "./message-row";

// 隔离剪贴板副作用：只断言 copyTextWithToast 被以正文调用。
// 用 importOriginal 展开原模块再覆盖这一个导出 —— 整包替换会连带把
// COPY_TOAST_DURATION_MS 等同源导出一起打空。
const copySpy = vi.fn();
vi.mock("../lib/clipboard-toast", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/clipboard-toast")>()),
  copyTextWithToast: (text: string, opts: unknown) => {
    copySpy(text, opts);
    return Promise.resolve(true);
  },
}));

describe("MessageRow", () => {
  it("Given an avatar node, When rendered, Then it leads the row as the avatar column", () => {
    const { container } = render(
      <MessageRow avatar={<span aria-label="me-pill">我</span>} name={null}>
        正文
      </MessageRow>,
    );

    expect(screen.getByLabelText("me-pill")).toBeInTheDocument();
    // 头像必须排在内容列之前 —— 后续分片行的幽灵 gutter 按这个次序对齐。
    expect(container.querySelector("article")?.firstElementChild).toBe(
      screen.getByLabelText("me-pill"),
    );
    expect(screen.getByText("正文")).toBeInTheDocument();
  });

  it("Given a name, When rendered, Then the header shows it above the body", () => {
    render(
      <MessageRow avatar={<span aria-label="avatar" />} name="后端">
        正文
      </MessageRow>,
    );

    expect(screen.getByText("后端")).toBeInTheDocument();
    expect(screen.getByText("正文")).toBeInTheDocument();
  });

  it("Given a footer, When rendered, Then the footer slot is mounted", () => {
    render(
      <MessageRow
        avatar={<span aria-label="avatar" />}
        name="后端"
        footer={<span>页脚</span>}
      >
        正文
      </MessageRow>,
    );

    expect(screen.getByText("页脚")).toBeInTheDocument();
  });

  it("Given align end, When rendered, Then the row is mirrored to the end edge", () => {
    const { container } = render(
      <MessageRow avatar={<span aria-label="avatar" />} name={null} align="end">
        正文
      </MessageRow>,
    );

    const article = container.querySelector("article");
    expect(article?.getAttribute("data-align")).toBe("end");
    expect(article?.className).toContain("flex-row-reverse");
    expect(article?.lastElementChild?.className).toContain("items-end");
  });

  it("Given no align, When rendered, Then the row keeps the start layout", () => {
    const { container } = render(
      <MessageRow avatar={<span aria-label="avatar" />} name={null}>
        正文
      </MessageRow>,
    );

    const article = container.querySelector("article");
    expect(article?.hasAttribute("data-align")).toBe(false);
    expect(article?.className).not.toContain("flex-row-reverse");
  });
});

describe("MessageCopyButton", () => {
  it("Given text, When clicked, Then copyTextWithToast is called with that text and the package-namespaced success title", () => {
    copySpy.mockClear();
    render(<MessageCopyButton text="hello world" />);

    fireEvent.click(screen.getByRole("button"));

    expect(copySpy).toHaveBeenCalledWith("hello world", expect.any(Object));
    // 默认标题走 useUiTranslation()，证明 common.copied 落在包的 namespace 上
    // 而不是宿主的 common（宿主删掉那条 key 后才暴露的那类失败）。
    expect(copySpy.mock.calls[0][1]).toMatchObject({ successTitle: "Copied" });
  });

  it("Given iconOnly, When rendered, Then it is a 44px icon button named by its aria-label", () => {
    render(<MessageCopyButton text="hello" ariaLabel="复制输出" iconOnly />);

    const button = screen.getByRole("button", { name: "复制输出" });
    expect(button.textContent).toBe("");
    expect(button.className).toContain("size-11");
  });

  it("Given empty text, When rendered, Then nothing is mounted", () => {
    const { container } = render(<MessageCopyButton text="" />);

    expect(container.querySelector("button")).toBeNull();
  });
});
