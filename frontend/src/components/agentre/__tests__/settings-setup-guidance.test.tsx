import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation, useNavigate } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

// 后端编辑器订阅 remote.device.state 才能让刚收编的机器就地由灰变可选，而这里渲染的是
// 整个设置页，那条订阅会跟着挂上。不桩 Wails runtime，EventsOn 会去读 undefined 上的
// EventsOnMultiple —— 与被测行为无关的一声炸响。
vi.mock("../../../../wailsjs/runtime/runtime", () => ({
  EventsOn: () => () => {},
}));

import { SettingsPage } from "../settings";
import { useChatAgentsStore } from "@/stores/chat-agents-store";

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname}</output>;
}

function SettingsStateNavigator() {
  const navigate = useNavigate();
  return (
    <button
      type="button"
      onClick={() =>
        navigate("/settings", { state: { settingsPage: "llm-providers" } })
      }
    >
      Open provider settings
    </button>
  );
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

function renderSettings({ withStateNavigator = false } = {}) {
  return render(
    <MemoryRouter initialEntries={["/settings"]}>
      <LocationProbe />
      {withStateNavigator ? <SettingsStateNavigator /> : null}
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

  it("applies a settings deep-link when navigation stays on /settings", async () => {
    const user = userEvent.setup();
    seedAgents([]);
    renderSettings({ withStateNavigator: true });

    await user.click(
      screen.getByRole("button", { name: "Open provider settings" }),
    );

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

  describe("LLM provider page gap banner", () => {
    it("shows the banner with a Go to org chart link when a provider gap exists", async () => {
      const user = userEvent.setup();
      seedAgents([
        {
          id: 1,
          name: "Eng",
          chattable: false,
          blockReason: "provider-inactive",
        },
      ]);
      renderSettings();

      await user.click(
        within(settingsNav()).getByRole("button", { name: "LLM Providers" }),
      );

      const alert = await screen.findByRole("alert");
      expect(
        within(alert).getByText("Some agents still need this"),
      ).toBeInTheDocument();

      await user.click(
        within(alert).getByRole("button", { name: "Go to org chart" }),
      );

      expect(screen.getByTestId("location")).toHaveTextContent("/org");
    });

    it("hides the banner when no agent lacks a provider", async () => {
      const user = userEvent.setup();
      seedAgents([{ id: 1, name: "CEO", chattable: true }]);
      renderSettings();

      await user.click(
        within(settingsNav()).getByRole("button", { name: "LLM Providers" }),
      );

      await screen.findByText("No LLM providers configured");
      expect(
        screen.queryByText("Some agents still need this"),
      ).not.toBeInTheDocument();
    });
  });
});
