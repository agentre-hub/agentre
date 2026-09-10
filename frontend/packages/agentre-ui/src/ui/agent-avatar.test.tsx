import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { AgentAvatar } from "./agent-avatar";

describe("AgentAvatar", () => {
  describe("三档身份", () => {
    it("给了上传头像就整块换成图片：不上底色、裁切填满", () => {
      render(
        <AgentAvatar
          name="Agentre"
          color="agent-7"
          avatarDataUrl="data:image/png;base64,AAAA"
        />,
      );

      const img = screen.getByRole("img", { name: "Agentre" });
      // 头像那一档不该再叠一层身份色 —— 图片本身就是身份。
      expect(img.style.backgroundColor).toBe("");
      const inner = img.querySelector("img");
      expect(inner?.getAttribute("src")).toBe("data:image/png;base64,AAAA");
    });

    it("给了图标节点就在色块上画那枚图标，不画首字母", () => {
      render(
        <AgentAvatar
          name="Agentre"
          color="agent-7"
          icon={<span data-testid="host-icon">🚀</span>}
        />,
      );

      const glyph = screen.getByRole("img", { name: "Agentre" });
      expect(screen.getByTestId("host-icon")).toBeInTheDocument();
      expect(glyph).not.toHaveTextContent("A");
      expect(glyph.style.backgroundColor).toBe("var(--agent-7)");
    });

    it("两样都没有就画首字母", () => {
      render(<AgentAvatar name="Agentre" color="agent-7" />);

      const glyph = screen.getByRole("img", { name: "Agentre" });
      expect(glyph).toHaveTextContent("A");
      expect(glyph.style.backgroundColor).toBe("var(--agent-7)");
    });
  });

  describe("首字母算法（与桌面端 getInitials 同一套）", () => {
    it("拉丁多词名取前两词首字母并大写", () => {
      render(<AgentAvatar name="code reviewer" />);

      expect(
        screen.getByRole("img", { name: "code reviewer" }),
      ).toHaveTextContent("CR");
    });

    it("三词以上只取前两词", () => {
      render(<AgentAvatar name="a b c" />);

      expect(screen.getByRole("img", { name: "a b c" })).toHaveTextContent(
        "AB",
      );
    });

    it("拉丁词后面跟着中文词时只取首字 —— 拼出来的「C助」在方块里放不下会折行", () => {
      render(<AgentAvatar name="CEO 助手" />);

      expect(screen.getByRole("img", { name: "CEO 助手" })).toHaveTextContent(
        /^C$/,
      );
    });

    it("非拉丁开头的名字取首字，不拼两个", () => {
      render(<AgentAvatar name="评审 助手" />);

      expect(screen.getByRole("img", { name: "评审 助手" })).toHaveTextContent(
        "评",
      );
    });

    it("名字为空时给 ?，可及名也为空 —— 不编一个具体身份出来", () => {
      const { container } = render(<AgentAvatar name="" testId="empty" />);

      expect(screen.getByTestId("empty")).toHaveTextContent("?");
      // 空名字不该被读屏念成某个 Agent。
      expect(container.querySelector("[aria-label='']")).not.toBeNull();
    });

    it("调用方给了 initials 就用它，不再自己算", () => {
      render(<AgentAvatar name="Agentre" initials="a" testId="explicit" />);

      // 项目字形要的是原样首字（不大写），所以这一层不能替调用方大写。
      expect(screen.getByTestId("explicit")).toHaveTextContent("a");
    });
  });

  describe("颜色兜底（spec 决策 5）", () => {
    it("颜色缺失时退回 agent-1，而不是中性面", () => {
      render(<AgentAvatar name="Agentre" testId="missing" />);

      expect(screen.getByTestId("missing").style.backgroundColor).toBe(
        "var(--agent-1)",
      );
    });

    it("颜色是空串时同样退回 agent-1", () => {
      render(<AgentAvatar name="Agentre" color="" testId="blank" />);

      expect(screen.getByTestId("blank").style.backgroundColor).toBe(
        "var(--agent-1)",
      );
    });

    it("neutral 是调色板里正当的灰，不退 agent-1", () => {
      // 用户在颜色选择器里明确选了灰（project_entity.allowedColors 十七个之一），
      // 把它当成「没有颜色」会让选的灰渲染成蓝。
      render(<AgentAvatar name="Agentre" color="neutral" testId="neutral" />);

      const glyph = screen.getByTestId("neutral");
      expect(glyph.style.backgroundColor).toBe("");
      expect(glyph.className).toContain("bg-secondary");
    });

    it("解析不出的 token 走中性面，且前景色跟着换 —— 白字落在浅灰面上读不出来", () => {
      render(<AgentAvatar name="Agentre" color="#3B82F6" testId="bogus" />);

      const glyph = screen.getByTestId("bogus");
      expect(glyph.style.backgroundColor).toBe("");
      expect(glyph.className).toContain("bg-secondary");
      expect(glyph.className).toContain("text-secondary-foreground");
      expect(glyph.className).not.toContain("text-agent-foreground");
    });
  });

  describe("四档尺寸", () => {
    it.each([
      ["xs", "size-3.5"],
      ["sm", "size-6"],
      ["md", "size-8"],
      ["lg", "size-10"],
    ] as const)("%s 档是 %s", (size, expected) => {
      render(<AgentAvatar name="Agentre" size={size} testId="sized" />);

      expect(screen.getByTestId("sized").className).toContain(expected);
    });

    it("缺省是 md —— 与桌面端今天的默认档一致", () => {
      render(<AgentAvatar name="Agentre" testId="default" />);

      expect(screen.getByTestId("default").className).toContain("size-8");
    });

    it("className 仍能盖掉尺寸：索引行里那一枚今天就是这么画的", () => {
      render(
        <AgentAvatar
          name="Agentre"
          size="sm"
          className="size-full"
          testId="override"
        />,
      );

      expect(screen.getByTestId("override").className).toContain("size-full");
    });
  });
});
