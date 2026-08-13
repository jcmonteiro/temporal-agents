/** The operator's chosen source for the interface color theme. */
export type ThemePreference = "light" | "dark" | "system";

/** The color theme that is currently rendered. */
export type Theme = Exclude<ThemePreference, "system">;

/** Resolves a preference against the operating system color-scheme setting. */
export function resolveTheme(preference: ThemePreference, systemPrefersDark: boolean): Theme {
  if (preference === "system") return systemPrefersDark ? "dark" : "light";
  return preference;
}
