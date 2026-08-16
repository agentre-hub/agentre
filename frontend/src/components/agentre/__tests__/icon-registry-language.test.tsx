import { render, screen, act } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import i18n from "@/i18n";
import enCommon from "@/i18n/locales/en/common.json";
import zhCommon from "@/i18n/locales/zh-CN/common.json";

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

describe("icon registry localization", () => {
  afterEach(async () => {
    await i18n.changeLanguage("en");
  });

  it("Given the registry is read in English, When the language switches to zh-CN, Then category labels follow the new language", async () => {
    expect(engineeringLabel()).toBe(
      enCommon.iconRegistry.categories.engineering,
    );

    await i18n.changeLanguage("zh-CN");

    expect(engineeringLabel()).toBe(
      zhCommon.iconRegistry.categories.engineering,
    );
  });

  it("Given the registry is read in English, When the language switches to zh-CN, Then icon labels follow the new language", async () => {
    expect(hammerLabel()).toBe(enCommon.iconRegistry.icons.hammer);

    await i18n.changeLanguage("zh-CN");

    expect(hammerLabel()).toBe(zhCommon.iconRegistry.icons.hammer);
  });

  it("Given search matches localized aliases, When the language switches to zh-CN, Then the zh-CN alias finds the icon and the en alias no longer does", async () => {
    expect(
      searchIcons(enCommon.iconRegistry.aliases.construction).map((m) => m.key),
    ).toContain("hammer");

    await i18n.changeLanguage("zh-CN");

    expect(
      searchIcons(zhCommon.iconRegistry.aliases.construction).map((m) => m.key),
    ).toContain("hammer");
    expect(
      searchIcons(enCommon.iconRegistry.aliases.construction).map((m) => m.key),
    ).not.toContain("hammer");
  });

  it("Given a rendered IconPicker showing the selected icon label, When the language switches to zh-CN, Then the trigger label is re-rendered in the new language", async () => {
    render(
      <IconPicker value="hammer" onChange={() => {}} accentColor="agent-1" />,
    );

    expect(
      screen.getByText(enCommon.iconRegistry.icons.hammer),
    ).toBeInTheDocument();

    await act(async () => {
      await i18n.changeLanguage("zh-CN");
    });

    expect(
      screen.getByText(zhCommon.iconRegistry.icons.hammer),
    ).toBeInTheDocument();
  });
});
