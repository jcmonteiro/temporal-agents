import { expect, it } from "vitest";
import { resolveTheme } from "./theme";

it("an explicit light preference keeps the interface light", () => {
  expect(resolveTheme("light", true)).toBe("light");
});

it("an explicit dark preference keeps the interface dark", () => {
  expect(resolveTheme("dark", false)).toBe("dark");
});

it("the system preference follows a dark operating system", () => {
  expect(resolveTheme("system", true)).toBe("dark");
});

it("the system preference follows a light operating system", () => {
  expect(resolveTheme("system", false)).toBe("light");
});
