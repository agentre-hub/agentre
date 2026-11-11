import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { FailureDetail, failureDetailText } from "./failure-detail";

describe("FailureDetail", () => {
  it("Given 机器原话, When 渲染, Then 原文在默认折叠的详情里，等宽且可选中", () => {
    const { container } = render(
      <FailureDetail detail="dial tcp 127.0.0.1:1: connection refused" />,
    );

    const details = container.querySelector("details");
    expect(details).not.toBeNull();
    expect(details!.open).toBe(false);
    expect(screen.getByText("Details").tagName).toBe("SUMMARY");

    const raw = screen.getByText("dial tcp 127.0.0.1:1: connection refused");
    expect(details!.contains(raw)).toBe(true);
    expect(raw.getAttribute("data-selectable-text")).toBe("true");
    // 不加可见的复制按钮：只读文本靠选中复制。
    expect(container.querySelector("button")).toBeNull();
  });

  it.each([undefined, "", "   "])(
    "Given 没有原文 (%j), When 渲染, Then 什么都不渲染",
    (detail) => {
      const { container } = render(<FailureDetail detail={detail} />);
      expect(container.firstChild).toBeNull();
    },
  );
});

describe("failureDetailText", () => {
  it.each<[string, unknown, string]>([
    ["Error", new Error("boom"), "boom"],
    ["裸字符串", "plain", "plain"],
    ["带 message 的对象", { message: "from object" }, "from object"],
    ["带 error 的对象", { error: "from error" }, "from error"],
    ["认不出的对象", {}, ""],
    ["undefined", undefined, ""],
    ["空白字符串", "  ", ""],
  ])("%s", (_name, err, expected) => {
    expect(failureDetailText(err)).toBe(expected);
  });
});
