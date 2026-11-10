import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";

import { AgentBackendsPanel } from "./agent-backends";
import type { BackendView, EngineSettingsPorts } from "./ports";

// 编辑器整页档（手机）：宿主管列表页与编辑页之间的导航，面板一次只渲染一层；
// 编辑页是「✕ · 标题 · 保存」的整屏页，字段按 基本 / 运行方式 / 高级 分组，
// 字段、校验与保存端口与弹窗档是同一份。
function setup() {
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
  const updateBackend = vi.fn().mockResolvedValue(backend);
  const ports = {
    listBackends: vi.fn().mockResolvedValue([backend]),
    listProviders: vi.fn().mockResolvedValue([]),
    listModels: vi.fn().mockResolvedValue([]),
    localDeviceFingerprint: vi.fn().mockResolvedValue("sha256:self"),
    listRuntimeDevices: vi.fn().mockResolvedValue([]),
    listAccountDevices: vi.fn().mockResolvedValue([]),
    updateBackend,
    cliPath: { get: vi.fn().mockResolvedValue(""), set: vi.fn() },
    canEditEnvJSON: true,
  } as unknown as EngineSettingsPorts;
  return { ports, updateBackend };
}

describe("AgentBackendsPanel editor page mode", () => {
  it("Given layer=editor with a backend id, When it renders, Then the editor is a full page with ✕ · title · Save and fields grouped Basics / Runtime / Advanced, without the list or a dialog", async () => {
    const { ports } = setup();
    render(
      <AgentBackendsPanel
        ports={ports}
        layer="editor"
        selectedId={7}
        onSelect={vi.fn()}
      />,
    );

    const page = await screen.findByRole("form", {
      name: "Edit Agent Backend",
    });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByRole("list", { name: /backends/i })).toBeNull();

    const close = within(page).getByRole("button", { name: "Close" });
    const save = within(page).getByRole("button", { name: "Save" });
    expect(close).toHaveClass("size-11");
    expect(save).toHaveClass("h-11");

    const basics = within(page).getByRole("region", { name: "Basics" });
    expect(within(basics).getByDisplayValue("My Claude")).toBeInTheDocument();
    expect(
      within(basics).getByRole("combobox", { name: "Runtime Device" }),
    ).toBeInTheDocument();
    expect(
      within(basics).getByRole("button", { name: "Model binding" }),
    ).toBeInTheDocument();

    const runtime = within(page).getByRole("region", { name: "Runtime" });
    expect(
      within(runtime).getByRole("combobox", {
        name: "Default Permission Mode",
      }),
    ).toBeInTheDocument();

    const advanced = within(page).getByRole("region", { name: "Advanced" });
    expect(within(advanced).getByText("CLI Path")).toBeInTheDocument();
    expect(
      within(advanced).getByRole("button", {
        name: /Custom Environment Variables/,
      }),
    ).toBeInTheDocument();
  });

  it("Given the editor page, When Save is pressed, Then the same update port runs and the host is told to leave the editor", async () => {
    const { ports, updateBackend } = setup();
    const onSelect = vi.fn();
    const user = userEvent.setup();
    render(
      <AgentBackendsPanel
        ports={ports}
        layer="editor"
        selectedId={7}
        onSelect={onSelect}
      />,
    );

    const page = await screen.findByRole("form", {
      name: "Edit Agent Backend",
    });
    await user.click(within(page).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(updateBackend).toHaveBeenCalledWith(
        7,
        expect.objectContaining({ name: "My Claude" }),
      ),
    );
    await waitFor(() => expect(onSelect).toHaveBeenCalledWith(null));
  });

  it("Given the editor page, When ✕ is pressed, Then nothing is saved and the host is told to leave the editor", async () => {
    const { ports, updateBackend } = setup();
    const onSelect = vi.fn();
    const user = userEvent.setup();
    render(
      <AgentBackendsPanel
        ports={ports}
        layer="editor"
        selectedId={7}
        onSelect={onSelect}
      />,
    );

    const page = await screen.findByRole("form", {
      name: "Edit Agent Backend",
    });
    await user.click(within(page).getByRole("button", { name: "Close" }));

    expect(onSelect).toHaveBeenCalledWith(null);
    expect(updateBackend).not.toHaveBeenCalled();
  });

  it("Given layer=editor with selectedId=new, When it renders, Then the create page renders", async () => {
    const { ports } = setup();
    render(
      <AgentBackendsPanel
        ports={ports}
        layer="editor"
        selectedId="new"
        onSelect={vi.fn()}
      />,
    );

    expect(
      await screen.findByRole("form", { name: "New Agent Backend" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("Given layer=editor with an id that is not in the list, When backends load, Then the host is told to clear the selection", async () => {
    const { ports } = setup();
    const onSelect = vi.fn();
    render(
      <AgentBackendsPanel
        ports={ports}
        layer="editor"
        selectedId={99}
        onSelect={onSelect}
      />,
    );

    await waitFor(() => expect(onSelect).toHaveBeenCalledWith(null));
    expect(screen.queryByRole("form")).toBeNull();
  });

  it("Given layer=editor and the CLI path lookup fails, When it settles, Then the host is sent back to the list and the list says why", async () => {
    const { ports } = setup();
    (ports.cliPath!.get as ReturnType<typeof vi.fn>).mockRejectedValue(
      new Error("cli overlays unreachable"),
    );
    const onSelect = vi.fn();
    const { rerender } = render(
      <AgentBackendsPanel
        ports={ports}
        layer="editor"
        selectedId={7}
        onSelect={onSelect}
      />,
    );

    await waitFor(() => expect(onSelect).toHaveBeenCalledWith(null));
    rerender(
      <AgentBackendsPanel
        ports={ports}
        layer="list"
        selectedId={null}
        onSelect={onSelect}
      />,
    );
    expect(
      await screen.findByText(/cli overlays unreachable/),
    ).toBeInTheDocument();
  });

  it("Given layer=editor while the CLI path is still being read, When it renders, Then a loading line holds the page instead of a blank screen", async () => {
    const { ports } = setup();
    (ports.cliPath!.get as ReturnType<typeof vi.fn>).mockReturnValue(
      new Promise(() => {}),
    );
    render(
      <AgentBackendsPanel
        ports={ports}
        layer="editor"
        selectedId={7}
        onSelect={vi.fn()}
      />,
    );

    await waitFor(() => expect(ports.cliPath!.get).toHaveBeenCalled());
    expect(screen.getByText("Loading...")).toBeInTheDocument();
  });

  it("Given layer=list, When Edit or New is pressed, Then the host is asked to open that editor and no dialog opens", async () => {
    const { ports } = setup();
    const onSelect = vi.fn();
    const user = userEvent.setup();
    render(
      <AgentBackendsPanel
        ports={ports}
        layer="list"
        selectedId={null}
        onSelect={onSelect}
        renderHeader={(actions) => <div>{actions}</div>}
      />,
    );

    await user.click(
      await screen.findByRole("button", { name: "Edit My Claude" }),
    );
    expect(onSelect).toHaveBeenLastCalledWith(7);

    await user.click(screen.getByRole("button", { name: "New Backend" }));
    expect(onSelect).toHaveBeenLastCalledWith("new");

    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByRole("form")).toBeNull();
  });

  it("Given layer=list, When Change binding is pressed, Then the editor page the host opens next comes up with the model picker open (Edit does not)", async () => {
    const { ports } = setup();
    const onSelect = vi.fn();
    const user = userEvent.setup();
    const props = {
      ports,
      onSelect,
      renderHeader: (actions: ReactNode) => <div>{actions}</div>,
    };
    const { rerender } = render(
      <AgentBackendsPanel {...props} layer="list" selectedId={null} />,
    );

    await user.click(
      await screen.findByRole("button", {
        name: "Change binding for My Claude",
      }),
    );
    expect(onSelect).toHaveBeenLastCalledWith(7);
    rerender(<AgentBackendsPanel {...props} layer="editor" selectedId={7} />);
    await screen.findByRole("form", { name: "Edit Agent Backend" });
    expect(
      await screen.findByPlaceholderText("Search providers or models…"),
    ).toBeInTheDocument();

    // ✕ 回到列表，再走「编辑」：这一次不该自动弹出选择器。
    rerender(<AgentBackendsPanel {...props} layer="list" selectedId={null} />);
    await user.click(
      await screen.findByRole("button", { name: "Edit My Claude" }),
    );
    rerender(<AgentBackendsPanel {...props} layer="editor" selectedId={7} />);
    await screen.findByRole("form", { name: "Edit Agent Backend" });
    expect(
      screen.queryByPlaceholderText("Search providers or models…"),
    ).toBeNull();
  });

  it("Given no layer, When Edit is pressed, Then the editor still opens as a dialog without groups", async () => {
    const { ports } = setup();
    const user = userEvent.setup();
    render(<AgentBackendsPanel ports={ports} />);

    await user.click(
      await screen.findByRole("button", { name: "Edit My Claude" }),
    );

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByRole("region")).toBeNull();
    expect(within(dialog).getByRole("button", { name: "Save" })).toBeVisible();
  });
});

// 草稿保护是宿主显式开启的（规格 2026-11-10 B7 / Compatibility）：不传 guardDraft 的
// 宿主（agentre-server）点遮罩照旧关窗；保存中不可关闭则是两端共同的默认行为。
describe("AgentBackendsPanel editor dialog draft guard", () => {
  // Radix 在打开后的下一拍才挂上 document 的 pointerdown 监听，所以先等一拍。
  async function pointerDownOnOverlay() {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    fireEvent.pointerDown(
      document.querySelector(
        '[data-slot="dialog-shell-overlay"]',
      ) as HTMLElement,
      { button: 0 },
    );
  }

  async function openEditorWithDraft(guardDraft?: boolean) {
    const { ports } = setup();
    const user = userEvent.setup();
    render(<AgentBackendsPanel ports={ports} guardDraft={guardDraft} />);
    await user.click(
      await screen.findByRole("button", { name: "Edit My Claude" }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByDisplayValue("My Claude"), " 2");
  }

  it("Given a host that did not opt in, When the backdrop is clicked on a typed draft, Then the dialog closes as it always did", async () => {
    await openEditorWithDraft();

    await pointerDownOnOverlay();

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("Given a host that passes guardDraft, When the backdrop is clicked on a typed draft, Then the dialog stays open", async () => {
    await openEditorWithDraft(true);

    await pointerDownOnOverlay();

    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });
});
