import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import * as pkg from "../index";
import { TouchProvider, useTouch } from "./touch";

function Probe({ touch }: { touch?: boolean }) {
  return <span data-testid="probe">{String(useTouch(touch))}</span>;
}

describe("触屏档位上下文", () => {
  it("不包 Provider 时是桌面档", () => {
    render(<Probe />);
    expect(screen.getByTestId("probe")).toHaveTextContent("false");
  });

  it("Provider 打开触屏档；显式入参优先于上下文", () => {
    render(
      <TouchProvider>
        <Probe />
        <Probe touch={false} />
      </TouchProvider>,
    );
    const [a, b] = screen.getAllByTestId("probe");
    expect(a).toHaveTextContent("true");
    expect(b).toHaveTextContent("false");
  });

  it("从 barrel 出得去", () => {
    expect(["TouchProvider", "useTouch"].filter((n) => !(n in pkg))).toEqual(
      [],
    );
  });
});
