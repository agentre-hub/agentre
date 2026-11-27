import { render, renderHook, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { agentreUiResources } from "../i18n";

import { BackendRow } from "./agent-backends-list";
import {
  cliBinaryName,
  isCLIPathBackend,
  reservedEnvKeysFor,
  type Backend,
  type Provider,
  type Translate,
} from "./agent-backends-shared";
import { BackendTypePicker } from "./agent-backends-badges";
import { ModelBindingSection } from "./agent-backends-fields";
import { LlmProviderLogo } from "./ai-brand-logo";
import { buildBackendDraft, matchingProviders } from "./backend-editor/draft";
import { computeSaveValidation } from "./backend-editor/save-validation";
import { useCliProbes } from "./backend-editor/use-cli-probes";
import {
  endpointFor,
  providerTypeMeta,
  providerTypeOrder,
} from "./llm-provider-models";
import { providerCompatibleForBackend } from "./model-target-picker";

// spec 2026-11-25-antigravity-backend：gemini 供应商只归 antigravity 后端；
// antigravity 必须绑定 gemini 供应商，env_json 里 GEMINI_API_KEY /
// GOOGLE_GEMINI_BASE_URL 只对它保留。

const t = ((key: string) => key) as unknown as Translate;

function provider(type: string, key = type): Provider {
  return {
    id: key.length,
    providerKey: key,
    name: key,
    type,
    baseUrl: "",
    enabled: true,
  } as unknown as Provider;
}

const ALL_PROVIDERS = [
  provider("anthropic"),
  provider("openai-chat"),
  provider("openai-response"),
  provider("gemini"),
];

type Locale = keyof typeof agentreUiResources;
function lookup(locale: Locale, path: string): unknown {
  return path
    .split(".")
    .reduce<unknown>(
      (node, part) =>
        node && typeof node === "object"
          ? (node as Record<string, unknown>)[part]
          : undefined,
      agentreUiResources[locale],
    );
}

describe("gemini provider type", () => {
  it("is offered in the provider editor with a badge and the Google default endpoint", () => {
    expect(providerTypeOrder).toContain("gemini");
    expect(providerTypeMeta.gemini.badge).not.toBe("");
    expect(providerTypeMeta.gemini.defaultBaseUrl).toBe(
      "https://generativelanguage.googleapis.com",
    );
  });

  it("falls back to the Google endpoint when the base URL is blank", () => {
    expect(endpointFor(provider("gemini"))).toBe(
      "https://generativelanguage.googleapis.com",
    );
  });

  it.each(["en", "zh-CN"] as const)("has a %s label", (locale) => {
    expect(lookup(locale, "llmProviders.providerType.gemini.label")).toBe(
      "Gemini",
    );
    expect(
      lookup(locale, "llmProviders.providerType.gemini.description"),
    ).toEqual(expect.any(String));
  });

  it("renders the Gemini brand mark even for an unrecognisable relay name", () => {
    const { container } = render(
      <LlmProviderLogo providerType="gemini" providerName="huu" />,
    );
    expect(container.querySelector('[data-brand="gemini"]')).not.toBeNull();
  });
});

describe("provider ↔ backend matching", () => {
  it("antigravity lists only gemini providers", () => {
    expect(
      matchingProviders("antigravity", ALL_PROVIDERS).map((p) => p.type),
    ).toEqual(["gemini"]);
    expect(providerCompatibleForBackend("antigravity", "gemini")).toBe(true);
    expect(providerCompatibleForBackend("antigravity", "anthropic")).toBe(
      false,
    );
  });

  it.each(["builtin", "claudecode", "codex", "piagent", "hermes", "openclaw"])(
    "%s never lists gemini providers",
    (backendType) => {
      expect(
        matchingProviders(
          backendType as Parameters<typeof matchingProviders>[0],
          ALL_PROVIDERS,
        ).some((p) => p.type === "gemini"),
      ).toBe(false);
      expect(providerCompatibleForBackend(backendType, "gemini")).toBe(false);
    },
  );

  it("builtin and piagent keep their existing provider types", () => {
    expect(
      matchingProviders("builtin", ALL_PROVIDERS).map((p) => p.type),
    ).toEqual(["anthropic", "openai-chat", "openai-response"]);
    expect(
      matchingProviders("piagent", ALL_PROVIDERS).map((p) => p.type),
    ).toEqual(["anthropic", "openai-chat", "openai-response"]);
  });
});

describe("antigravity backend editor", () => {
  it("is offered in the type picker with its brand mark and the Antigravity name", () => {
    render(
      <BackendTypePicker
        value="claudecode"
        onChange={vi.fn()}
        probes={{}}
        canCreateBuiltin
      />,
    );
    const option = screen.getByRole("radio", { name: /Antigravity/ });
    expect(option.getAttribute("data-backend-type")).toBe("antigravity");
    expect(option.querySelector('[data-brand="antigravity"]')).not.toBeNull();
  });

  it.each(["en", "zh-CN"] as const)(
    "has %s copy named Antigravity",
    (locale) => {
      expect(
        lookup(locale, "agentBackends.backendType.antigravity.label"),
      ).toBe("Antigravity");
      expect(
        lookup(locale, "agentBackends.backendType.antigravity.shortLabel"),
      ).toBe("Antigravity");
    },
  );

  it("probes the agy executable like the other CLI backends", async () => {
    expect(isCLIPathBackend("antigravity")).toBe(true);
    expect(cliBinaryName("antigravity")).toBe("agy");
    const resolve = vi.fn().mockResolvedValue({ found: true, path: "/bin/x" });
    const { result } = renderHook(() =>
      useCliProbes({
        stateKind: "create",
        initialCliPath: "",
        type: "antigravity",
        deviceId: "",
        resolveCliPath: resolve,
        t,
      }),
    );
    await waitFor(() =>
      expect(result.current.cliProbes.antigravity).toEqual({
        state: "installed",
        path: "/bin/x",
      }),
    );
    expect(resolve).toHaveBeenCalledWith(
      expect.objectContaining({ type: "antigravity" }),
    );
  });

  function validate(llmProviderKey: string, reservedOffenders: string[] = []) {
    const gemini = provider("gemini", "g-1");
    return computeSaveValidation({
      type: "antigravity",
      name: "Antigravity",
      llmProviderKey,
      llmModelKey: "",
      cliPath: "",
      hermesUrl: "",
      openClawGatewayURL: "",
      targetCatalog: [
        {
          providerKey: "g-1",
          id: 1,
          name: "g-1",
          type: "gemini",
          enabled: true,
          defaultModel: {
            modelKey: "m-1",
            modelId: "gemini-3-pro",
            enabled: true,
          },
          models: [],
        },
      ],
      filteredProviders: [gemini],
      reservedOffenders,
      t,
    }).effectiveSaveBlockedReason;
  }

  it("cannot be saved without a provider", () => {
    expect(validate("")).toBe("agentBackends.summary.reasons.bindingRequired");
    expect(validate("g-1")).toBeNull();
  });

  it("blocks saving while a reserved env key is present", () => {
    expect(validate("g-1", ["GEMINI_API_KEY"])).toBe(
      "agentBackends.env.reservedDisabled",
    );
  });

  it("reserves GEMINI_API_KEY and GOOGLE_GEMINI_BASE_URL for antigravity only", () => {
    const antigravity = reservedEnvKeysFor("antigravity");
    expect(antigravity.has("GEMINI_API_KEY")).toBe(true);
    expect(antigravity.has("GOOGLE_GEMINI_BASE_URL")).toBe(true);
    // 公共保留键对它同样生效（agent_backend.go 对所有类型统一校验）。
    expect(antigravity.has("ANTHROPIC_BASE_URL")).toBe(true);
    for (const other of ["claudecode", "codex", "piagent", "hermes"] as const) {
      const keys = reservedEnvKeysFor(other);
      expect(keys.has("GEMINI_API_KEY")).toBe(false);
      expect(keys.has("GOOGLE_GEMINI_BASE_URL")).toBe(false);
      expect(keys.has("ANTHROPIC_BASE_URL")).toBe(true);
    }
  });

  it("drafts only the channels agy has: cli path, env and the provider binding", () => {
    const draft = buildBackendDraft({
      type: "antigravity",
      name: "Antigravity",
      deviceId: "dev-1",
      llmProviderKey: "g-1",
      llmModelKey: "m-1",
      cliPath: " /opt/agy ",
      routes: {
        OPUS: { providerKey: "g-1", modelKey: "m-1" },
        SONNET: { providerKey: "", modelKey: "" },
        HAIKU: { providerKey: "", modelKey: "" },
      },
      sandbox: "read-only",
      approval: "never",
      envEntries: [{ key: "FOO", value: "1" }],
      reasoningEffort: "high",
      defaultPermissionMode: "plan",
      defaultModel: "gemini-x",
      openClawGatewayURL: "",
      openClawAgentID: "",
      openClawDefaultModel: "",
      hermesUrl: "",
      hermesAuthProvider: "",
      hermesUserId: "",
    });
    expect(draft).toMatchObject({
      deviceId: "dev-1",
      llmProviderKey: "g-1",
      llmModelKey: "m-1",
      cliPath: "/opt/agy",
      envJson: '{"FOO":"1"}',
      modelRoutes: {},
      sandbox: "",
      approval: "",
      defaultPermissionMode: "",
      defaultModel: "",
      // agy 自己的思考档位不在 Agentre 里显示，ACP 也没有这条通道。
      reasoningEffort: "",
    });
  });
});

describe("antigravity backend row", () => {
  // 它没有 CLI 自身登录这条退路：没绑供应商就是失效，不能读成「走 CLI 登录」。
  it("reads an unbound antigravity backend as invalid, not as CLI login", () => {
    render(
      <BackendRow
        backend={
          {
            id: 1,
            name: "agy",
            type: "antigravity",
            llmProviderKey: "",
            llmProviderActive: false,
            agentCount: 0,
          } as unknown as Backend
        }
        hasLocalDevice
        testing={false}
        testDisabled={false}
        onTest={vi.fn()}
        onCancelTest={vi.fn()}
        onEdit={vi.fn()}
        onChangeBinding={vi.fn()}
        onDelete={vi.fn()}
      />,
    );
    const binding = screen.getByTestId("backend-binding");
    expect(binding.textContent).toContain("Model binding is invalid");
    expect(binding.textContent).not.toContain("CLI login");
  });
});

describe("antigravity model binding", () => {
  // 必填：区块标题不带「可选」，没有 gemini 供应商时提示先去配置，而不是允许空着保存。
  it("is not marked optional and asks for a provider when none is compatible", () => {
    render(
      <ModelBindingSection
        type="antigravity"
        providers={[]}
        value=""
        modelKey=""
        onTargetChange={vi.fn()}
        invalid={false}
        piAgentModelMissing={false}
        editing={false}
        catalog={[]}
        catalogLoading={false}
        catalogError={false}
        executionLocation=""
        supportsFixedModel
        routes={{
          OPUS: { providerKey: "", modelKey: "" },
          SONNET: { providerKey: "", modelKey: "" },
          HAIKU: { providerKey: "", modelKey: "" },
        }}
        onRoutesChange={vi.fn()}
        customModel=""
        onCustomModelChange={vi.fn()}
        resolvedMainTarget={{
          mode: "native",
          providerName: "",
          providerType: "",
          modelLabel: "",
        }}
        openPickerOnMount={false}
      />,
    );
    const block = screen.getByTestId("model-binding-block");
    expect(block.textContent).not.toContain("Optional");
    expect(block.textContent).toContain("No LLM providers available yet");
  });

  function renderBinding(type: "antigravity" | "claudecode") {
    return render(
      <ModelBindingSection
        type={type}
        providers={[provider("gemini", "gem")]}
        value=""
        modelKey=""
        onTargetChange={vi.fn()}
        invalid={false}
        piAgentModelMissing={false}
        editing={false}
        catalog={[
          {
            providerKey: "gem",
            id: 1,
            name: "huu-gemini",
            type: "gemini",
            enabled: true,
            defaultModel: {
              modelKey: "m-1",
              modelId: "gemini-3-pro",
              enabled: true,
            },
            models: [],
          },
        ]}
        catalogLoading={false}
        catalogError={false}
        executionLocation=""
        supportsFixedModel
        routes={{
          OPUS: { providerKey: "", modelKey: "" },
          SONNET: { providerKey: "", modelKey: "" },
          HAIKU: { providerKey: "", modelKey: "" },
        }}
        onRoutesChange={vi.fn()}
        customModel=""
        onCustomModelChange={vi.fn()}
        resolvedMainTarget={{
          mode: "native",
          providerName: "",
          providerType: "",
          modelLabel: "",
        }}
        openPickerOnMount
      />,
    );
  }

  it("does not offer CLI login and shows a provider placeholder for an empty draft", () => {
    renderBinding("antigravity");
    const text = document.body.textContent ?? "";
    expect(text).not.toContain("CLI login");
    expect(text).toContain("Choose a Gemini provider");
  });

  it("keeps the CLI login option for other backend types", () => {
    renderBinding("claudecode");
    expect(document.body.textContent).toContain("CLI login");
    expect(document.body.textContent).not.toContain("Choose a Gemini provider");
  });
});
