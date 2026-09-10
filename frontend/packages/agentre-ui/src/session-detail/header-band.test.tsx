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
  it("四态同一副外壳：高度写死，标题长短与身份认不认得出都不改变它", () => {
    const { rerender } = render(
      <SessionHeaderBand
        testId="band"
        avatar={<span data-testid="avatar" />}
        title="很长很长的一个标题，长到要折成两行才放得下的那一种"
      />,
    );
    const shell = screen.getByTestId("band");
    expect(shell.className).toContain("h-[68px]");
    // 窄档降级按**这一带的实际宽度**分，而不是靠折行把头部撑高。
    expect(shell.className).toContain("@container/header");

    rerender(
      <SessionHeaderBand
        testId="band"
        avatar={<span data-testid="avatar" />}
        title="短"
      />,
    );
    expect(screen.getByTestId("band").className).toContain("h-[68px]");
  });

  it("分隔符只夹在真的存在的两段之间，行首不留一个孤零零的「·」", () => {
    render(
      <SessionHeaderBand
        testId="band"
        metaTestId="band-meta"
        avatar={<span />}
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
        avatar={<span />}
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
        avatar={<span />}
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
        avatar={<span />}
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
        avatar={<span />}
        meta={[{ key: "agent", node: <span>Atlas</span> }]}
      />,
    );

    expect(screen.queryByRole("heading")).toBeNull();
    expect(screen.getByTestId("band-meta").textContent).toBe("Atlas");
  });

  it("leading 排在头像之前，右端那一组排在最后", () => {
    render(
      <SessionHeaderBand
        testId="band"
        leading={<button type="button">返回</button>}
        avatar={<span data-testid="avatar" />}
        title="标题"
        actions={<button type="button">停止</button>}
      />,
    );

    const shell = screen.getByTestId("band");
    const order = [...shell.children].map((el) => el.tagName);
    expect(order[0]).toBe("BUTTON");
    expect(shell.children[1]).toBe(screen.getByTestId("avatar"));
    expect(shell.children[shell.children.length - 1]?.textContent).toBe("停止");
  });

  it("一段 meta 都没有时不画那一行", () => {
    render(
      <SessionHeaderBand
        testId="band"
        metaTestId="band-meta"
        avatar={<span />}
        title="标题"
      />,
    );

    expect(screen.queryByTestId("band-meta")).toBeNull();
  });
});
