import { screen } from "@testing-library/react";
import { renderWithPorts as render } from "./__testing__/ports";
import { describe, expect, it } from "vitest";

import { StreamingMarkdown } from "./markdown-text";

// StreamingMarkdown 增量渲染流式 markdown:把已定稿 block 与活跃尾巴拆成多个
// 独立的(React.memo 的)内层 ReactMarkdown,只有尾巴每 chunk 重解析。这里验证:
//   1. 渲染结果与一次性整段解析等价(内容不丢、代码块仍渲染);
//   2. 所有 block 共享同一个 .markdown-body 容器 —— 段落的 first/last 外边距重置
//      必须跨整条消息生效,否则已定稿段落间距会塌成 0,turn done 整段重渲染时
//      又撑开,产生「流式挤在一起、done 时啪地展开」的跳变。
describe("StreamingMarkdown", () => {
  it("when streaming text has prose + closed code + tail then renders all content", () => {
    const { container } = render(
      <StreamingMarkdown
        text={"para one\n\n```js\nconst x = 1;\n```\n\ntail growing"}
      />,
    );
    expect(screen.getByText("para one")).toBeInTheDocument();
    // 代码经 highlight.js 拆进多个 span,文本被切碎 —— 用 textContent 整体断言。
    expect(container.textContent).toContain("const x = 1;");
    expect(screen.getByText("tail growing")).toBeInTheDocument();
  });

  it("when text has multiple committed blocks then they share a single markdown-body wrapper", () => {
    // 单一容器是间距与一次性解析保持一致的结构前提。
    const { container } = render(
      <StreamingMarkdown text={"para one\n\npara two\n\ntail growing"} />,
    );
    expect(container.querySelectorAll(".markdown-body")).toHaveLength(1);
    // 三个段落都在同一容器里,作为兄弟节点参与 first/last 外边距计算。
    expect(screen.getByText("para one")).toBeInTheDocument();
    expect(screen.getByText("para two")).toBeInTheDocument();
    expect(screen.getByText("tail growing")).toBeInTheDocument();
  });

  it("when text is a single growing block then it renders a single markdown-body wrapper", () => {
    const { container } = render(<StreamingMarkdown text="just growing" />);
    expect(container.querySelectorAll(".markdown-body")).toHaveLength(1);
  });

  it("Given an incomplete trusted filename in the active tail, when a later chunk completes it, then only the complete target becomes a link", () => {
    const view = render(
      <StreamingMarkdown text="Inspect main.g" cwd="/work/proj" />,
    );
    expect(screen.queryByRole("link")).toBeNull();

    view.rerender(
      <StreamingMarkdown text="Inspect main.go" cwd="/work/proj" />,
    );
    expect(screen.getByRole("link", { name: /main\.go/ })).toBeInTheDocument();
  });

  it("Given an autolink in a committed segment, when only the streaming tail grows, then both committed and completed targets remain visible", () => {
    const view = render(
      <StreamingMarkdown
        text={"README.md\n\nInspect main.g"}
        cwd="/work/proj"
      />,
    );
    expect(
      screen.getByRole("link", { name: /README\.md/ }),
    ).toBeInTheDocument();

    view.rerender(
      <StreamingMarkdown
        text={"README.md\n\nInspect main.go"}
        cwd="/work/proj"
      />,
    );

    expect(
      screen.getByRole("link", { name: /README\.md/ }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /main\.go/ })).toBeInTheDocument();
  });
});
