// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { detectLocale, localeStorageKey, messages } from "./i18n";

afterEach(() => localStorage.clear());

describe("admin locale", () => {
  it("keeps both dictionaries structurally identical", () => {
    expect(Object.keys(messages["en-US"]).sort()).toEqual(Object.keys(messages["zh-CN"]).sort());
  });

  it("honors the explicit locale preference", () => {
    localStorage.setItem(localeStorageKey, "en-US");
    expect(detectLocale()).toBe("en-US");
    localStorage.setItem(localeStorageKey, "zh-CN");
    expect(detectLocale()).toBe("zh-CN");
  });
});
