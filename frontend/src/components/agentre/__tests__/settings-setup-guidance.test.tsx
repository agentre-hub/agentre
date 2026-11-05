import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

// 后端编辑器订阅 remote.device.state 才能让刚收编的机器就地由灰变可选，而这里渲染的是
// 整个设置页，那条订阅会跟着挂上。不桩 Wails runtime，EventsOn 会去读 undefined 上的
// EventsOnMultiple —— 与被测行为无关的一声炸响。
vi.mock("../../../../wailsjs/runtime/runtime", () => ({
  EventsOn: () => () => {},
}));

import { SettingsPage } from "../settings";
import { useChatAgentsStore } from "@/stores/chat-agents-store";
import {
  openSettings,
  useSettingsDialogStore,
} from "@/stores/settings-dialog-store";

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname}</output>;
}

type SeedAgent = {
  id: number;
  name: string;
  chattable: boolean;
  blockReason?: string;
  hasBackendTarget?: boolean;
};

function seedAgents(agents: SeedAgent[]) {
  useChatAgentsStore.setState({
    agents: agents.map((a) => ({
      id: a.id,
      name: a.name,
      avatarColor: "agent-1",
      backendType: "builtin",
      chattable: a.chattable,
      blockReason: a.blockReason ?? "",
      hasBackendTarget: a.hasBackendTarget ?? false,
      pinned: false,
      sessions: [],
      attentionSessions: [],
      sessionIds: [],
    })) as never,
    loading: false,
    error: null,
  });
}

function renderSettings() {
  return render(
    <MemoryRouter initialEntries={["/settings"]}>
      <LocationProbe />
      <SettingsPage
        effectiveTheme="light"
        onThemePreferenceChange={() => {}}
        themePreference="system"
      />
    </MemoryRouter>,
  );
}

function settingsNav() {
  return screen.getByRole("complementary", { name: "Settings" });
}

describe("Settings setup guidance (task 7)", () => {
  beforeEach(() => {
    useSettingsDialogStore.setState({ open: false, page: "appearance" });
    useChatAgentsStore.getState().__reset();
    vi.spyOn(useChatAgentsStore.getState(), "reload").mockResolvedValue();
  });

  describe("nav warning dots", () => {
    it("Agent 后端 shows a dot when some agent has no backend; LLM 供应商 shows none", async () => {
      seedAgents([
        { id: 1, name: "CEO", chattable: false, blockReason: "no-backend" },
      ]);
      renderSettings();

      await waitFor(() => {
        const nav = settingsNav();
        expect(
          nav.querySelector('[data-gap-dot="agent-backend"]'),
        ).not.toBeNull();
        expect(nav.querySelector('[data-gap-dot="llm-providers"]')).toBeNull();
      });
    });

    it("Given an Agent target references a deleted backend, When settings renders, Then the hidden backend does not keep the warning dot lit", async () => {
      seedAgents([
        {
          id: 1,
          name: "CEO",
          chattable: false,
          blockReason: "no-backend",
          hasBackendTarget: true,
        },
      ]);
      renderSettings();

      await waitFor(() => {
        expect(
          settingsNav().querySelector('[data-gap-dot="agent-backend"]'),
        ).toBeNull();
      });
    });

    it("LLM 供应商 shows a dot when some agent is bound to an inactive provider; Agent 后端 shows none", async () => {
      seedAgents([
        {
          id: 1,
          name: "Eng",
          chattable: false,
          blockReason: "provider-inactive",
        },
      ]);
      renderSettings();

      await waitFor(() => {
        const nav = settingsNav();
        expect(nav.querySelector('[data-gap-dot="agent-backend"]')).toBeNull();
        expect(
          nav.querySelector('[data-gap-dot="llm-providers"]'),
        ).not.toBeNull();
      });
    });

    it("LLM 供应商 dot also covers backend-requires-provider agents", async () => {
      seedAgents([
        {
          id: 1,
          name: "Eng",
          chattable: false,
          blockReason: "backend-requires-provider",
        },
      ]);
      renderSettings();

      await waitFor(() => {
        const nav = settingsNav();
        expect(
          nav.querySelector('[data-gap-dot="llm-providers"]'),
        ).not.toBeNull();
      });
    });

    it("hides both dots when every agent is chattable", async () => {
      seedAgents([{ id: 1, name: "CEO", chattable: true }]);
      renderSettings();

      await waitFor(() => {
        const nav = settingsNav();
        expect(nav.querySelector('[data-gap-dot="agent-backend"]')).toBeNull();
        expect(nav.querySelector('[data-gap-dot="llm-providers"]')).toBeNull();
      });
    });
  });

  it("openSettings(page) lands the dialog on that sub-page", async () => {
    seedAgents([]);
    renderSettings();
    expect(
      await screen.findByRole("heading", { name: "Appearance" }),
    ).toBeInTheDocument();

    act(() => openSettings("llm-providers"));

    expect(
      await screen.findByRole("heading", { name: "LLM Providers" }),
    ).toBeInTheDocument();
  });

  describe("agent backend empty state", () => {
    it("renders the empty card with description and both action buttons", async () => {
      const user = userEvent.setup();
      seedAgents([]);
      renderSettings();

      await user.click(
        within(settingsNav()).getByRole("button", { name: "Agent Backends" }),
      );

      expect(
        await screen.findByText("No agent backends yet"),
      ).toBeInTheDocument();
      expect(
        screen.getByText(/Every agent must bind a backend to run tasks/),
      ).toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: "Add First Backend" }),
      ).toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: "Configure LLM providers first" }),
      ).toBeInTheDocument();
    });

    it("primary button opens the create-backend dialog", async () => {
      const user = userEvent.setup();
      seedAgents([]);
      renderSettings();

      await user.click(
        within(settingsNav()).getByRole("button", { name: "Agent Backends" }),
      );
      await user.click(
        await screen.findByRole("button", { name: "Add First Backend" }),
      );

      expect(
        await screen.findByRole("dialog", { name: "New Agent Backend" }),
      ).toBeInTheDocument();
    });

    it("secondary button navigates to the LLM providers page", async () => {
      const user = userEvent.setup();
      seedAgents([]);
      renderSettings();

      await user.click(
        within(settingsNav()).getByRole("button", { name: "Agent Backends" }),
      );
      await user.click(
        await screen.findByRole("button", {
          name: "Configure LLM providers first",
        }),
      );

      expect(
        await screen.findByRole("heading", { name: "LLM Providers" }),
      ).toBeInTheDocument();
    });
  });

  describe("LLM provider empty state", () => {
    it("shows the chain hint and a Go to Agent backends link that navigates there", async () => {
      const user = userEvent.setup();
      seedAgents([]);
      renderSettings();

      await user.click(
        within(settingsNav()).getByRole("button", { name: "LLM Providers" }),
      );

      expect(
        await screen.findByText("No LLM providers configured"),
      ).toBeInTheDocument();
      expect(
        screen.getByText("LLM providers → Agent backends → Agent chats"),
      ).toBeInTheDocument();

      await user.click(
        screen.getByRole("button", { name: "Go to Agent backends" }),
      );

      expect(
        await screen.findByRole("heading", { name: "Agent Backends" }),
      ).toBeInTheDocument();
    });
  });

  describe("LLM provider page gap list", () => {
    it("lists each agent missing a provider link, and 去配置 deep-links to that agent", async () => {
      const user = userEvent.setup();
      useSettingsDialogStore.setState({ open: true });
      seedAgents([
        {
          id: 1,
          name: "Eng",
          chattable: false,
          blockReason: "provider-inactive",
        },
        {
          id: 2,
          name: "Doc",
          chattable: false,
          blockReason: "backend-requires-provider",
        },
        // 缺的是后端不是供应商：不归这一页管
        { id: 3, name: "CEO", chattable: false, blockReason: "no-backend" },
        { id: 4, name: "Ops", chattable: true },
      ]);
      renderSettings();

      await user.click(
        within(settingsNav()).getByRole("button", { name: "LLM Providers" }),
      );

      const list = await screen.findByRole("list", { name: "Needs setup" });
      // 不再是一条解释性横幅
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      expect(
        screen.getByRole("heading", { name: "Needs setup · 2" }),
      ).toBeInTheDocument();
      const rows = within(list).getAllByRole("listitem");
      expect(rows).toHaveLength(2);
      expect(within(rows[0]).getByRole("img", { name: "Eng" })).toBeVisible();
      expect(within(rows[0]).getByText("Eng")).toBeInTheDocument();
      expect(
        within(rows[0]).getByText("Provider not activated"),
      ).toBeInTheDocument();
      expect(within(rows[1]).getByText("Doc")).toBeInTheDocument();
      expect(
        within(rows[1]).getByText("Backend has no provider"),
      ).toBeInTheDocument();
      expect(within(list).queryByText("CEO")).not.toBeInTheDocument();

      await user.click(
        within(rows[1]).getByRole("button", { name: "Configure Doc" }),
      );

      expect(screen.getByTestId("location")).toHaveTextContent("/org");
      // 落点就是这个 Agent：组织页记住的选中键已写好
      expect(localStorage.getItem("agentre.orgTree.selected")).toBe(
        JSON.stringify({ kind: "agent", id: 2 }),
      );
      // 先关设置再跳转：对话框不能带着自己落到组织页上。
      expect(useSettingsDialogStore.getState().open).toBe(false);
    });

    it("hides the list when no agent lacks a provider", async () => {
      const user = userEvent.setup();
      seedAgents([
        { id: 1, name: "CEO", chattable: true },
        { id: 2, name: "Ops", chattable: false, blockReason: "no-backend" },
      ]);
      renderSettings();

      await user.click(
        within(settingsNav()).getByRole("button", { name: "LLM Providers" }),
      );

      await screen.findByText("No LLM providers configured");
      expect(
        screen.queryByRole("list", { name: "Needs setup" }),
      ).not.toBeInTheDocument();
    });
  });
});
