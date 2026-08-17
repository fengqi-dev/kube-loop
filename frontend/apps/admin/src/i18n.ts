import { createContext, useContext } from "react";

export type Locale = "zh-CN" | "en-US";
export const localeStorageKey = "kubeloop.admin.locale";

const zh = {
  loading: "正在加载…",
  search: "搜索",
  close: "关闭",
  reason: "变更原因",
  operationReason: "请输入 8–512 个字符的操作原因",
  cancel: "取消",
  confirm: "确认",
};

const en: Record<keyof typeof zh, string> = {
  loading: "Loading…",
  search: "Search",
  close: "Close",
  reason: "Change reason",
  operationReason: "Enter an 8–512 character reason",
  cancel: "Cancel",
  confirm: "Confirm",
};

export type MessageKey = keyof typeof zh;
export const messages: Record<Locale, Record<MessageKey, string>> = {
  "zh-CN": zh,
  "en-US": en,
};

export function detectLocale(): Locale {
  const saved = localStorage.getItem(localeStorageKey);
  if (saved === "zh-CN" || saved === "en-US") return saved;
  return navigator.languages.some((language) => language.toLowerCase().startsWith("zh"))
    ? "zh-CN"
    : "en-US";
}

export const I18nContext = createContext({
  locale: "zh-CN" as Locale,
  setLocale: (_locale: Locale) => {},
  t: (key: MessageKey) => messages["zh-CN"][key],
});

export const useI18n = () => useContext(I18nContext);
