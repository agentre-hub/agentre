import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { lazyView } from "./lazy-boundary";

function Loaded() {
  return <div>loaded view</div>;
}

type Module = { default: React.ComponentType };

// 照浏览器的 module map 造的 import()：同一个地址第一次的结果（包括失败）被永久
// 缓存，网络恢复后再 import 同一个地址拿到的仍是那次失败。每次调用照样返回一个
// 新的 promise，只是结局沿用缓存。只有换一个文档（整页重载）才有新的 module map。
type Outcome = { ok: true } | { ok: false; error: Error };

function browserModuleMap() {
  const network = { up: false };
  let cache = new Map<string, Outcome>();
  const importModule = (url: string): Promise<Module> => {
    let outcome = cache.get(url);
    if (!outcome) {
      outcome = network.up
        ? { ok: true }
        : {
            ok: false,
            error: new TypeError("Failed to fetch dynamically imported module"),
          };
      cache.set(url, outcome);
    }
    return outcome.ok
      ? Promise.resolve({ default: Loaded })
      : Promise.reject(outcome.error);
  };
  const newDocument = () => {
    cache = new Map();
  };
  return { network, importModule, newDocument };
}

describe("lazyView", () => {
  it("renders the loaded component once the chunk resolves", async () => {
    const View = lazyView(() => Promise.resolve({ default: Loaded }));
    render(<View />);
    expect(await screen.findByText("loaded view")).toBeInTheDocument();
  });

  it("recovers on retry after the network comes back, although the browser cached the failed import", async () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    const browser = browserModuleMap();

    // 模块顶层的声明：整页重载时会重新求值。
    const declare = () =>
      lazyView(() => browser.importModule("/assets/page-3f2a.js"), {
        reload,
      });
    let View = declare();
    function Root() {
      return <View />;
    }
    // 整页重载 = 新文档（新 module map）+ 模块重新求值 + 重新挂载。
    function reload() {
      browser.newDocument();
      View = declare();
      view.unmount();
      view = render(<Root />);
    }
    let view = render(<Root />);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Something went wrong",
    );

    browser.network.up = true;
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: "Retry" }));

    expect(await screen.findByText("loaded view")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
    spy.mockRestore();
  });
});
