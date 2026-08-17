export type AuthLocale = "zh-CN" | "en-US";

export function authenticationError(
  locale: AuthLocale,
  code: string | null,
) {
  if (code !== "authentication_failed") return "";
  return locale === "zh-CN"
    ? "登录失败，请检查凭据后重试。"
    : "Sign-in failed. Check your credentials and try again.";
}
