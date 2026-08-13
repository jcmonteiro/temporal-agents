import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { resolveTheme, type Theme, type ThemePreference } from "../domain/theme";

const STORAGE_KEY = "agent-hub.theme-preference";
const DARK_SCHEME = "(prefers-color-scheme: dark)";

interface ThemeContextValue {
  preference: ThemePreference;
  theme: Theme;
  setPreference: (preference: ThemePreference) => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

/** Applies and retains the interface color-scheme preference for the hub. */
export function ThemeProvider({ children }: { children: ReactNode }): ReactNode {
  const [preference, setStoredPreference] = useState<ThemePreference>(readPreference);
  const [systemPrefersDark, setSystemPrefersDark] = useState(prefersDark);
  const theme = resolveTheme(preference, systemPrefersDark);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  useEffect(() => {
    const mediaQuery = window.matchMedia(DARK_SCHEME);
    const update = (event: MediaQueryListEvent): void => setSystemPrefersDark(event.matches);
    mediaQuery.addEventListener("change", update);
    return () => mediaQuery.removeEventListener("change", update);
  }, []);

  const value = useMemo<ThemeContextValue>(
    () => ({
      preference,
      theme,
      setPreference: (nextPreference) => {
        setStoredPreference(nextPreference);
        writePreference(nextPreference);
      },
    }),
    [preference, theme],
  );

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

/** Gives a component access to the global theme preference. */
export function useTheme(): ThemeContextValue {
  const value = useContext(ThemeContext);
  if (value === null) throw new Error("Theme controls must be inside ThemeProvider");
  return value;
}

function readPreference(): ThemePreference {
  try {
    const preference = window.localStorage.getItem(STORAGE_KEY);
    return isThemePreference(preference) ? preference : "system";
  } catch {
    return "system";
  }
}

function writePreference(preference: ThemePreference): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, preference);
  } catch {
    // The chosen theme still applies when storage is unavailable.
  }
}

function prefersDark(): boolean {
  return window.matchMedia(DARK_SCHEME).matches;
}

function isThemePreference(value: string | null): value is ThemePreference {
  return value === "light" || value === "dark" || value === "system";
}
