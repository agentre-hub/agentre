import { render } from "@testing-library/react";
import type * as React from "react";
import { describe, expect, it, vi } from "vitest";

const primitiveProps = vi.hoisted(() => ({
  content: null as Record<string, unknown> | null,
}));

// 只替换 DropdownMenu 一个命名空间,其余 radix 原语用 importOriginal 原样透出:
// `@/components/ui/dropdown-menu` 经 `@/lib/utils` 连到共享包的 barrel,那里还挂着
// HoverCard / Tooltip / Button 等同样吃 radix 的组件。整包替换会让它们在 import
// 期就拿到 undefined 而炸,与本用例要断言的定位参数毫无关系。
vi.mock("radix-ui", async (importOriginal) => {
  const Primitive = ({ children }: { children?: React.ReactNode }) => children;
  const Content = (props: Record<string, unknown>) => {
    primitiveProps.content = props;
    return <div>{props.children as React.ReactNode}</div>;
  };

  return {
    ...(await importOriginal<typeof import("radix-ui")>()),
    DropdownMenu: new Proxy(
      {},
      {
        get: (_target, property) =>
          property === "Content" ? Content : Primitive,
      },
    ),
  };
});

import { DropdownMenuContent } from "../index";

describe("DropdownMenuContent positioning", () => {
  it("Given a menu is close to a viewport edge, When it is positioned, Then the shared component enables flipping with a safe boundary", () => {
    render(<DropdownMenuContent>Agent</DropdownMenuContent>);

    expect(primitiveProps.content).toMatchObject({
      avoidCollisions: true,
      collisionPadding: 8,
      sticky: "always",
    });
  });
});
