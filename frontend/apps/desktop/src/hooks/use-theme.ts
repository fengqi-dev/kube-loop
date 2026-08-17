import {
  createContext,
  createElement,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import {
  WindowSetDarkTheme,
  WindowSetLightTheme,
} from "../../../../wailsjs/runtime/runtime";

export type ThemePreference = "light" | "dark" | "system";
export type ResolvedTheme = "light" | "dark";

const storageKey = "kube-loop.theme";

type ThemeContextValue = {
  preference: ThemePreference;
  resolved: ResolvedTheme;
  setPreference: (value: ThemePreference) => void;
};

const ThemeContext = createContext<ThemeContextValue | null>(null);

function readPreference(): ThemePreference {
  try {
    const value = localStorage.getItem(storageKey);
    if (value === "light" || value === "dark" || value === "system") {
      return value;
    }
  } catch {
    // ignore
  }
  return "light";
}

function systemPrefersDark() {
  return window.matchMedia("(prefers-color-scheme: dark)").matches;
}

function resolveTheme(preference: ThemePreference): ResolvedTheme {
  if (preference === "system") {
    return systemPrefersDark() ? "dark" : "light";
  }
  return preference;
}

function applyResolvedTheme(resolved: ResolvedTheme) {
  document.documentElement.classList.toggle("dark", resolved === "dark");
  document.documentElement.style.colorScheme = resolved;
  try {
    if (resolved === "dark") {
      WindowSetDarkTheme();
    } else {
      WindowSetLightTheme();
    }
  } catch {
    // runtime may be unavailable outside Wails
  }
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [preference, setPreferenceState] = useState<ThemePreference>(() =>
    typeof window === "undefined" ? "light" : readPreference(),
  );
  const [resolved, setResolved] = useState<ResolvedTheme>(() =>
    typeof window === "undefined" ? "light" : resolveTheme(readPreference()),
  );

  useEffect(() => {
    const next = resolveTheme(preference);
    setResolved(next);
    applyResolvedTheme(next);

    if (preference !== "system") {
      return;
    }
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => {
      const systemResolved = media.matches ? "dark" : "light";
      setResolved(systemResolved);
      applyResolvedTheme(systemResolved);
    };
    media.addEventListener("change", onChange);
    return () => media.removeEventListener("change", onChange);
  }, [preference]);

  const setPreference = useCallback((value: ThemePreference) => {
    setPreferenceState(value);
    try {
      localStorage.setItem(storageKey, value);
    } catch {
      // ignore
    }
  }, []);

  const value = useMemo(
    () => ({ preference, resolved, setPreference }),
    [preference, resolved, setPreference],
  );

  return createElement(ThemeContext.Provider, { value }, children);
}

export function useTheme() {
  const context = useContext(ThemeContext);
  if (!context) {
    throw new Error("useTheme must be used within ThemeProvider");
  }
  return context;
}
