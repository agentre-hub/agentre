import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { SessionHeaderBand } from "./header-band";

/** meta 行里各段出现的先后，按它们自报的 key。 */
function metaKeys(): string[] {
  return [...screen.getByTestId("band-meta").children].map(
    (part) => (part as HTMLElement).dataset.part ?? "",
  );
}

describe("SessionHeaderBand", () => {
  it("四态同一副外壳：56px 恒高、自己一层顶带底色，标题长短不改变它", () => {
    const { rerender } = render(
      <SessionHeaderBand
        testId="band"
        title="很长很长的一个标题，长到在一行里放不下、要收成省略号的那一种"
      />,
    );
    const shell = screen.getByTestId("band");
    expect(shell.className).toMatch(/(^|\s)h-14(\s|$)/);
    // 顶带自己一层底色：静止时就和下面的转录分得开，不再像第一条消息。
    expect(shell.className).toMatch(/(^|\s)bg-sheet-header(\s|$)/);
    // 窄档降级按**这一带的实际宽度**分，而不是靠折行把头部撑高。
    expect(shell.className).toContain("@container/header");

    rerender(<SessionHeaderBand testId="band" title="短" />);
    expect(screen.getByTestId("band").className).toMatch(/(^|\s)h-14(\s|$)/);
  });

  it("标题只占一行，全文在悬停 title 里", () => {
    const long = "很长很长的一个标题，长到在一行里放不下、要收成省略号的那一种";
    render(<SessionHeaderBand testId="band" title={long} />);

    const heading = screen.getByRole("heading", { level: 2 });
    expect(heading).toHaveClass("line-clamp-1");
    expect(heading).not.toHaveClass("line-clamp-2");
    expect(heading).toHaveAttribute("title", long);
  });

  it("分隔符只夹在真的存在的两段之间，行首不留一个孤零零的「·」", () => {
    render(
      <SessionHeaderBand
        testId="band"
        metaTestId="band-meta"
        title="标题"
        meta={[
          { key: "agent", node: <span>Atlas</span> },
          { key: "project", node: <span>Agentre</span> },
        ]}
      />,
    );

    expect(metaKeys()).toEqual(["agent", "project"]);
    const text = screen.getByTestId("band-meta").textContent ?? "";
    expect(text.startsWith("·")).toBe(false);
    expect(text).toContain("Atlas");
    expect(text).toContain("·");
    expect(text).toContain("Agentre");
  });

  it("只有一段时一个分隔符都不画", () => {
    render(
      <SessionHeaderBand
        testId="band"
        metaTestId="band-meta"
        meta={[{ key: "agent", node: <span>Atlas</span> }]}
      />,
    );

    expect(screen.getByTestId("band-meta").textContent).toBe("Atlas");
  });

  it("窄档先收哪一段由那一段自己带着", () => {
    render(
      <SessionHeaderBand
        testId="band"
        metaTestId="band-meta"
        meta={[
          { key: "agent", node: <span>Atlas</span> },
          {
            key: "machine",
            node: <span>书房小主机</span>,
            hideAt: "@max-[560px]/header:hidden",
          },
        ]}
      />,
    );

    const machine = screen.getByTestId("band-meta-machine");
    expect(machine.className).toContain("@max-[560px]/header:hidden");
    expect(screen.getByTestId("band-meta-agent").className).not.toContain(
      "hidden",
    );
  });

  it("每段的 testId 由 metaTestId 推出来，给了显式的就用显式那个", () => {
    render(
      <SessionHeaderBand
        testId="band"
        metaTestId="band-meta"
        meta={[
          { key: "agent", node: <span>Atlas</span> },
          { key: "topline", node: <span>主干</span>, testId: "band-topline" },
        ]}
      />,
    );

    expect(screen.getByTestId("band-meta-agent")).toBeTruthy();
    expect(screen.getByTestId("band-topline")).toBeTruthy();
  });

  it("不给标题就不画那一行：路由页形态的标题在壳的顶栏上，这里再印一遍是重复", () => {
    render(
      <SessionHeaderBand
        testId="band"
        metaTestId="band-meta"
        meta={[{ key: "agent", node: <span>Atlas</span> }]}
      />,
    );

    expect(screen.queryByRole("heading")).toBeNull();
    expect(screen.getByTestId("band-meta").textContent).toBe("Atlas");
  });

  it("没有 32px 身份格：leading 之后直接是标题列，右端那一组排在最后", () => {
    render(
      <SessionHeaderBand
        testId="band"
        leading={<button type="button">返回</button>}
        title="标题"
        actions={<button type="button">停止</button>}
      />,
    );

    const shell = screen.getByTestId("band");
    expect(shell.children).toHaveLength(3);
    expect(shell.children[0]?.tagName).toBe("BUTTON");
    expect(shell.children[1]).toContainElement(
      screen.getByRole("heading", { level: 2 }),
    );
    expect(shell.children[2]?.textContent).toBe("停止");
  });

  it("一段 meta 都没有时不画那一行", () => {
    render(
      <SessionHeaderBand testId="band" metaTestId="band-meta" title="标题" />,
    );

    expect(screen.queryByTestId("band-meta")).toBeNull();
  });
});
