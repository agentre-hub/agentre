import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import i18n from "i18next";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AGENTRE_UI_NAMESPACE, agentreUiResources } from "../i18n";
import { PermissionModePill } from "./permission-mode-pill";

const CLAUDECODE = ["default", "acceptEdits", "plan", "bypassPermissions"];

afterEach(async () => {
  if (i18n.language !== "en") await i18n.changeLanguage("en");
});

async function openPill(modes: string[] = CLAUDECODE, props = {}) {
  const onSelect = vi.fn();
  render(
    <PermissionModePill
      mode="default"
      modes={modes}
      onSelect={onSelect}
      {...props}
    />,
  );
  await userEvent.click(
    screen.getByRole("button", { name: /Permission mode/ }),
  );
  return onSelect;
}

describe("PermissionModePill", () => {
  it("列出 runtime 报的那几档，每档带自己的说明", async () => {
    await openPill();
    const options = screen.getAllByRole("option");
    expect(options.map((o) => o.getAttribute("aria-selected"))).toEqual([
      "true",
      "false",
      "false",
      "false",
    ]);
    expect(
      screen.getByText("Read-only analysis without write or side-effect tools"),
    ).toBeInTheDocument();
  });

  // 只报两档的 runtime（codex）就只列两档 —— 档位集合是 runtime 说了算的，
  // 不是前端按 backend 类型猜的。
  it("runtime 只报两档时就只列两档", async () => {
    await openPill(["default", "plan"]);
    expect(screen.getAllByRole("option")).toHaveLength(2);
  });

  // 认不出的档不阻断：标签退成 key、无说明、中性样式。加新 backend 时前端不用先改。
  it("认不出的档走兜底渲染而不是报错", async () => {
    await openPill(["default", "brandNewMode"]);
    expect(
      screen.getByRole("option", { name: /brandNewMode/ }),
    ).toBeInTheDocument();
  });

  it("会话已启动且不是以 bypass 起手时，bypass 档禁用并写明原因", async () => {
    const onSelect = await openPill(CLAUDECODE, {
      runtimeKey: "claudecode",
      hasActiveSession: true,
      permissionModeAtLaunch: "",
    });
    const bypass = screen.getByRole("option", { name: /Bypass/ });
    expect(bypass).toBeDisabled();
    expect(bypass).toHaveAttribute(
      "title",
      expect.stringContaining("did not start with bypassPermissions"),
    );
    await userEvent.click(bypass);
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("以 bypass 起手的会话，bypass 档照常可选", async () => {
    const onSelect = await openPill(CLAUDECODE, {
      runtimeKey: "claudecode",
      hasActiveSession: true,
      permissionModeAtLaunch: "bypassPermissions",
    });
    await userEvent.click(screen.getByRole("option", { name: /Bypass/ }));
    expect(onSelect).toHaveBeenCalledWith("bypassPermissions");
  });

  // 这条规则绑死在 claudecode CLI 的行为上，别的 runtime 不该跟着受限。
  it("codex 不触发 bypass 锁死", async () => {
    const onSelect = await openPill(CLAUDECODE, {
      runtimeKey: "codex",
      hasActiveSession: true,
      permissionModeAtLaunch: "",
    });
    await userEvent.click(screen.getByRole("option", { name: /Bypass/ }));
    expect(onSelect).toHaveBeenCalledWith("bypassPermissions");
  });

  // 这是相对搬进包之前的一处**修正**：档位说明此前在模块加载期就被 t() 定死，
  // 语言切换后一直停在首次加载时的那门语言上。
  it("切换语言后档位说明跟着变", async () => {
    i18n.addResourceBundle(
      "zh-CN",
      AGENTRE_UI_NAMESPACE,
      agentreUiResources["zh-CN"],
    );
    await openPill();
    expect(
      screen.getByText("Read-only analysis without write or side-effect tools"),
    ).toBeInTheDocument();

    await i18n.changeLanguage("zh-CN");
    await waitFor(() => {
      expect(
        screen.getByText("只读分析，不执行任何写入或副作用工具"),
      ).toBeInTheDocument();
    });
  });
});

describe("PermissionModePill · 禁用原因", () => {
  // 禁用有不止一个来路。宿主给了具体原因就说那一句，否则才回到默认的
  // 「生成中不可切换」——对着一台答不出档位的机器说「生成中不可切换」是句
  // 与事实无关的话。
  it("宿主给了原因就说那一句", () => {
    render(
      <PermissionModePill
        mode="default"
        modes={CLAUDECODE}
        onSelect={vi.fn()}
        disabled
        disabledReason="这台机器此刻列不出权限档位"
      />,
    );
    expect(
      screen.getByRole("button", { name: /Permission mode/ }),
    ).toHaveAttribute("title", "这台机器此刻列不出权限档位");
  });

  it("没给原因时回到默认文案", () => {
    render(
      <PermissionModePill
        mode="default"
        modes={CLAUDECODE}
        onSelect={vi.fn()}
        disabled
      />,
    );
    expect(
      screen.getByRole("button", { name: /Permission mode/ }),
    ).toHaveAttribute("title", expect.stringContaining("Cannot switch"));
  });
});
