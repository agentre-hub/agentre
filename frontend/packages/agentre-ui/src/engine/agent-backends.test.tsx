// W9：Agent 后端清单读失败时不出「还没有后端」空态（会诱导重复新建），原位说明 + 重试。
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { TouchProvider } from "../ui/touch";
import { AgentBackendsPanel } from "./agent-backends";
import type { BackendView, EngineSettingsPorts } from "./ports";

const backend = {
  id: 7,
  syncId: "cc-7",
  name: "My Claude",
  type: "claudecode",
  llmProviderKey: "",
  llmModelKey: "",
  envJson: "{}",
  deviceId: "",
  defaultModel: "",
  modelRoutes: {},
  cliByDevice: [],
  agentCount: 0,
} as unknown as BackendView;

function ports(listBackends: EngineSettingsPorts["listBackends"]) {
  return {
    listBackends,
    listProviders: vi.fn().mockResolvedValue([]),
    listModels: vi.fn().mockResolvedValue([]),
    localDeviceFingerprint: vi.fn().mockResolvedValue("sha256:self"),
    listRuntimeDevices: vi.fn().mockResolvedValue([]),
    listAccountDevices: vi.fn().mockResolvedValue([]),
    cliPath: { get: vi.fn().mockResolvedValue(""), set: vi.fn() },
  } as unknown as EngineSettingsPorts;
}

const failThenOk = () =>
  vi
    .fn()
    .mockRejectedValueOnce(new Error("request timed out after 15000ms"))
    .mockResolvedValue([backend]);

describe("AgentBackendsPanel 读失败（W9）", () => {
  it("清单读失败：不出空态与「添加第一个」，原位说明 + 重试，重试后出清单", async () => {
    const user = userEvent.setup();
    const listBackends = failThenOk();
    render(<AgentBackendsPanel ports={ports(listBackends)} />);

    const notice = await screen.findByTestId("backend-load-failure");
    expect(notice).toHaveTextContent("Agent backends didn't load");
    // 读失败只说本地化的人话：上面那条 flash 与它的「详情」都不再带原文。
    expect(document.body.textContent).not.toMatch(/timed out after|15000/);
    expect(screen.queryByText("No agent backends yet")).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Add First Backend" }),
    ).toBeNull();

    await user.click(within(notice).getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("My Claude")).toBeInTheDocument();
    expect(screen.queryByTestId("backend-load-failure")).toBeNull();
  });

  it("真的没有后端：仍是空态，没有失败说明", async () => {
    render(<AgentBackendsPanel ports={ports(vi.fn().mockResolvedValue([]))} />);
    expect(
      await screen.findByText("No agent backends yet"),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("backend-load-failure")).toBeNull();
  });

  it("触屏：重试 44px；桌面是紧凑按钮", async () => {
    const { unmount } = render(
      <AgentBackendsPanel ports={ports(failThenOk())} />,
    );
    expect(
      within(await screen.findByTestId("backend-load-failure")).getByRole(
        "button",
        { name: "Retry" },
      ),
    ).not.toHaveClass("min-h-11");
    unmount();
    render(
      <TouchProvider>
        <AgentBackendsPanel ports={ports(failThenOk())} />
      </TouchProvider>,
    );
    expect(
      within(await screen.findByTestId("backend-load-failure")).getByRole(
        "button",
        { name: "Retry" },
      ),
    ).toHaveClass("min-h-11");
  });
});
