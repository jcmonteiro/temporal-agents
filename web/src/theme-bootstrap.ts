// This module runs before React so the first rendered frame has theme tokens.
try {
  const preference = localStorage.getItem("agent-hub.theme-preference");
  const theme =
    preference === "light" || preference === "dark"
      ? preference
      : matchMedia("(prefers-color-scheme: dark)").matches
        ? "dark"
        : "light";
  document.documentElement.dataset.theme = theme;
} catch {
  document.documentElement.dataset.theme = "light";
}
