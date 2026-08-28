import { render, screen, act } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { agentreUiResources } from "@agentre-hub/agentre-ui";

import i18n from "@/i18n";

import { IconPicker } from "../icon-picker";
import { iconsByCategory, searchIcons } from "../icon-registry";

function engineeringLabel(): string | undefined {
  return iconsByCategory().find((g) => g.category === "engineering")?.label;
}

function hammerLabel(): string | undefined {
  return iconsByCategory()
    .find((g) => g.category === "engineering")
    ?.items.find((m) => m.key === "hammer")?.label;
}

/**
 * 词表已搬进共享包（`packages/agentre-ui/src/org/icon-registry.ts`），文案也随之
 * 进了包的 `agentreUi` bundle。这里守的是**宿主适配层**：包的取值函数接在宿主唯一
 * 的那个 i18next 实例上，语言一变，图标选择器里的文案跟着变。
 */
describe("icon registry localization", () => {
  afterEach(async () => {
    await i18n.changeLanguage("en");
  });

  it("Given the registry is read in English, When the language switches to zh-CN, Then category labels follow the new language", async () => {
    expect(engineeringLabel()).toBe(
      agentreUiResources.en.iconRegistry.categories.engineering,
    );

    await i18n.changeLanguage("zh-CN");

    expect(engineeringLabel()).toBe(
      agentreUiResources["zh-CN"].iconRegistry.categories.engineering,
    );
  });

  it("Given the registry is read in English, When the language switches to zh-CN, Then icon labels follow the new language", async () => {
    expect(hammerLabel()).toBe(agentreUiResources.en.iconRegistry.icons.hammer);

    await i18n.changeLanguage("zh-CN");

    expect(hammerLabel()).toBe(
      agentreUiResources["zh-CN"].iconRegistry.icons.hammer,
    );
  });

  it("Given search matches localized aliases, When the language switches to zh-CN, Then the zh-CN alias finds the icon and the en alias no longer does", async () => {
    expect(
      searchIcons(agentreUiResources.en.iconRegistry.aliases.construction).map(
        (m) => m.key,
      ),
    ).toContain("hammer");

    await i18n.changeLanguage("zh-CN");

    expect(
      searchIcons(
        agentreUiResources["zh-CN"].iconRegistry.aliases.construction,
      ).map((m) => m.key),
    ).toContain("hammer");
    expect(
      searchIcons(agentreUiResources.en.iconRegistry.aliases.construction).map(
        (m) => m.key,
      ),
    ).not.toContain("hammer");
  });

  it("Given a rendered IconPicker showing the selected icon label, When the language switches to zh-CN, Then the trigger label is re-rendered in the new language", async () => {
    render(
      <IconPicker value="hammer" onChange={() => {}} accentColor="agent-1" />,
    );

    expect(
      screen.getByText(agentreUiResources.en.iconRegistry.icons.hammer),
    ).toBeInTheDocument();

    await act(async () => {
      await i18n.changeLanguage("zh-CN");
    });

    expect(
      screen.getByText(agentreUiResources["zh-CN"].iconRegistry.icons.hammer),
    ).toBeInTheDocument();
  });
});
