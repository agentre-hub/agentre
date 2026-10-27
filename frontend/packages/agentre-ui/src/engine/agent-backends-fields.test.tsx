import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import {
  CliPathField,
  DefaultPermissionModeField,
  EnvJsonField,
  HermesAuthFields,
  HermesFields,
  ModelBindingSection,
  ReasoningEffortField,
} from "./agent-backends-fields";

// spec 2026-09-01「三后端下发档位的收敛」+ 设计决策 2:档位表统一六档,删除
// REASONING_EFFORTS_CODEX 这条按后端裁剪的分支——编辑器对四个支持力度的后端
// (claudecode / codex / piagent / builtin，调用方按 type !== "openclaw" 门控是否
// 渲染这颗字段)呈现同一张六档表,codex 不再被单独藏 max。组件本身不再吃 type，
// 这张表因此对所有调用方恒等。
describe("Claude CLI model binding", () => {
  const base = {
    type: "claudecode" as const,
    providers: [],
    value: "",
    modelKey: "",
    onTargetChange: vi.fn(),
    invalid: false,
    piAgentModelMissing: false,
    editing: true,
    catalog: [],
    catalogLoading: false,
    catalogError: false,
    executionLocation: "",
    supportsFixedModel: true,
    routes: {
      OPUS: { providerKey: "", modelKey: "" },
      SONNET: { providerKey: "", modelKey: "" },
      HAIKU: { providerKey: "", modelKey: "" },
    },
    onRoutesChange: vi.fn(),
    customModel: "saved-manual-id",
    onCustomModelChange: vi.fn(),
    resolvedMainTarget: {
      mode: "native" as const,
      providerName: "",
      providerType: "",
      modelLabel: "",
    },
    openPickerOnMount: false,
  };
  it("Given a saved manual CLI model and no providers, When opening the binding picker, Then it is fixed and selectable without a separate custom-model input", async () => {
    render(
      <ModelBindingSection
        {...base}
        cliCatalog={{ models: [], effectiveModelId: "default-id" }}
        onRefreshCLIModels={vi.fn()}
      />,
    );
    expect(screen.queryByLabelText("Custom Model")).not.toBeInTheDocument();
    const trigger = screen.getByRole("button", { name: "Model binding" });
    expect(trigger).toHaveTextContent("Claude Code login account");
    expect(trigger).toHaveTextContent("Fixed");
    expect(trigger).toHaveTextContent("saved-manual-id");
    await userEvent.click(trigger);
    expect(
      screen.getByRole("option", { name: /saved-manual-id/ }),
    ).toHaveAttribute("aria-selected", "true");
    await userEvent.click(
      screen.getByRole("option", { name: /Follow CLI default/ }),
    );
    expect(base.onCustomModelChange).toHaveBeenLastCalledWith("");
    expect(base.onTargetChange).toHaveBeenLastCalledWith({
      providerKey: "",
      modelKey: "",
    });
  });
  it("Given CLI default mode, Then applied settings resolve the trigger and explain following future defaults", () => {
    render(
      <ModelBindingSection
        {...base}
        customModel=""
        cliCatalog={{
          models: [
            { value: "sonnet", label: "CLI Sonnet", modelId: "sonnet-id" },
          ],
          effectiveValue: "sonnet",
          effectiveModelId: "sonnet-id",
        }}
        onRefreshCLIModels={vi.fn()}
      />,
    );
    const trigger = screen.getByRole("button", { name: "Model binding" });
    expect(trigger).toHaveTextContent("CLI Sonnet · sonnet-id");
    expect(trigger).toHaveTextContent("Follows changes to the CLI default");
  });
});

describe("ReasoningEffortField", () => {
  it("展示同一张六档表(含 max)", async () => {
    render(<ReasoningEffortField value="" onChange={vi.fn()} />);
    await userEvent.click(screen.getByRole("combobox"));
    const options = screen.getAllByRole("option");
    expect(options).toHaveLength(6);
    expect(options.some((opt) => opt.textContent?.includes("max"))).toBe(true);
  });
});

/**
 * 远端 + bypassPermissions 那条提示（「agentred 以 root 跑会被 CLI 拒掉，设
 * IS_SANDBOX=1」）此前挂在 `canEditEnvJSON` 上。那颗开关同时还管着整个 env_json
 * 编辑器，而**浏览器宿主永远不能开它**——env_json 不下发（agentre-server 的 R19），
 * 编辑器拿不到现有的表，保存就会把用户自填的键连同他自己放的密钥一起抹掉。
 *
 * 于是提示与编辑器分成两颗开关：能不能**编辑整张表**（桌面端）与能不能**补这一个
 * 键**（两个宿主都能，只是走的路不同：桌面端改本地 entries，浏览器调服务端接口）。
 */
describe("DefaultPermissionModeField 的 IS_SANDBOX 提示", () => {
  const base = {
    value: "bypassPermissions",
    onChange: vi.fn(),
    isRemote: true,
    hasIsSandbox: false,
  };

  it("宿主能补这个键时出提示与按钮，即使它不能编辑整张 env 表", async () => {
    const onAddIsSandbox = vi.fn();
    render(
      <DefaultPermissionModeField
        {...base}
        canAddIsSandbox
        onAddIsSandbox={onAddIsSandbox}
      />,
    );

    const button = screen.getByRole("button", { name: /IS_SANDBOX/ });
    await userEvent.click(button);
    expect(onAddIsSandbox).toHaveBeenCalledTimes(1);
  });

  it("宿主补不了这个键时不出按钮", () => {
    render(
      <DefaultPermissionModeField
        {...base}
        canAddIsSandbox={false}
        onAddIsSandbox={vi.fn()}
      />,
    );

    expect(
      screen.queryByRole("button", { name: /IS_SANDBOX/ }),
    ).not.toBeInTheDocument();
  });

  /** 本机执行不走这条路：CLI 的 root 检查只在远端 agentred 那侧成立。 */
  it("本机执行时不出提示", () => {
    render(
      <DefaultPermissionModeField
        {...base}
        isRemote={false}
        canAddIsSandbox
        onAddIsSandbox={vi.fn()}
      />,
    );

    expect(
      screen.queryByRole("button", { name: /IS_SANDBOX/ }),
    ).not.toBeInTheDocument();
  });

  /** 已配置时显示确认态而不是按钮——桌面端读得到 env_json 才给得出这个状态。 */
  it("已配置时不出按钮", () => {
    render(
      <DefaultPermissionModeField
        {...base}
        hasIsSandbox
        canAddIsSandbox
        onAddIsSandbox={vi.fn()}
      />,
    );

    expect(
      screen.queryByRole("button", { name: /IS_SANDBOX/ }),
    ).not.toBeInTheDocument();
  });
});

describe("CliPathField", () => {
  it("Given an empty claudecode path, Then the existing $PATH fallback wording stays", () => {
    render(
      <CliPathField
        type="claudecode"
        value=""
        onChange={vi.fn()}
        onDetect={vi.fn()}
        detecting={false}
        missMessage={null}
      />,
    );

    expect(screen.getByText("Empty = claude from $PATH")).toBeInTheDocument();
  });
});

describe("HermesFields", () => {
  it("renders the Server URL as one controlled text input", () => {
    const onUrlChange = vi.fn();
    render(
      <HermesFields url="http://127.0.0.1:9119" onUrlChange={onUrlChange} />,
    );

    const url = screen.getByLabelText("Server URL");
    expect(url).toHaveValue("http://127.0.0.1:9119");

    fireEvent.change(url, { target: { value: "ws://127.0.0.1:9200" } });
    expect(onUrlChange).toHaveBeenLastCalledWith("ws://127.0.0.1:9200");
  });
});

describe("HermesAuthFields", () => {
  const base = {
    provider: "basic",
    onProviderChange: vi.fn(),
    username: "alice",
    onUsernameChange: vi.fn(),
    password: "secret",
    onPasswordChange: vi.fn(),
    userId: "",
    providers: [
      { name: "basic", displayName: "Basic", supportsPassword: true },
      { name: "oidc", displayName: "SSO", supportsPassword: false },
    ],
    providersLoading: false,
    providersError: "",
    loggingIn: false,
    error: "",
    onLogin: vi.fn(),
    onLogout: vi.fn(),
  };

  it("offers provider / username / password and a login button", async () => {
    const onLogin = vi.fn();
    render(<HermesAuthFields {...base} onLogin={onLogin} />);

    expect(screen.getByLabelText("Username")).toHaveValue("alice");
    expect(screen.getByLabelText("Password")).toHaveValue("secret");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(onLogin).toHaveBeenCalledTimes(1);
  });

  it("once signed in shows the identity and a sign-out button instead of a password field", () => {
    render(<HermesAuthFields {...base} userId="user-7" />);

    expect(screen.getByText("Signed in as user-7")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Sign out" }),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("Password")).not.toBeInTheDocument();
  });

  it("disables login and explains when the selected provider cannot use a password", () => {
    render(<HermesAuthFields {...base} provider="oidc" />);

    expect(
      screen.getByText(/requires browser authorization/i),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Sign in" })).toBeDisabled();
  });
});

// 保留键（agentre 自己写的那几个）在 env 表里只会被丢弃。顶上那条横幅说「哪几个键」，
// 行里的那一格还要自己标出来：无边框改版之后字段没有边可变红，标记走 aria-invalid
// （Input 把它画成破坏色的环，读屏也读得到）。
describe("EnvJsonField", () => {
  it("Given a reserved key and an ordinary key, When rendered, Then only the reserved key field is marked invalid", () => {
    render(
      <EnvJsonField
        entries={[
          { key: "ANTHROPIC_API_KEY", value: "x" },
          { key: "MY_FLAG", value: "1" },
        ]}
        onChange={vi.fn()}
        open
        onToggle={vi.fn()}
        reservedOffenders={["ANTHROPIC_API_KEY"]}
      />,
    );

    expect(screen.getByDisplayValue("ANTHROPIC_API_KEY")).toHaveAttribute(
      "aria-invalid",
      "true",
    );
    expect(screen.getByDisplayValue("MY_FLAG")).not.toHaveAttribute(
      "aria-invalid",
    );
  });
});
