// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ThemeProvider, useTheme } from "./theme";

let prefersDark = false;
let changeListener: ((event: MediaQueryListEvent) => void) | undefined;
let storage: Storage;
let localStorageDescriptor: PropertyDescriptor | undefined;

beforeEach(() => {
  const values = new Map<string, string>();
  storage = {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: (key) => values.delete(key),
    clear: () => values.clear(),
    key: () => null,
    get length() {
      return values.size;
    },
  };
  localStorageDescriptor = Object.getOwnPropertyDescriptor(window, "localStorage");
  Object.defineProperty(window, "localStorage", { configurable: true, value: storage });
  document.documentElement.removeAttribute("data-theme");
  prefersDark = false;
  changeListener = undefined;
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => ({
      matches: prefersDark,
      media: "(prefers-color-scheme: dark)",
      addEventListener: (_event: string, listener: (event: MediaQueryListEvent) => void) => {
        changeListener = listener;
      },
      removeEventListener: () => {},
    })),
  );
});

afterEach(() => {
  cleanup();
  if (localStorageDescriptor === undefined) {
    delete (window as { localStorage?: Storage }).localStorage;
  } else {
    Object.defineProperty(window, "localStorage", localStorageDescriptor);
  }
  vi.unstubAllGlobals();
});

function ThemeControls(): React.JSX.Element {
  const { preference, setPreference } = useTheme();
  return (
    <>
      <output>{preference}</output>
      <button type="button" onClick={() => setPreference("dark")}>
        Use dark
      </button>
    </>
  );
}

it("an explicit dark choice is applied and retained", () => {
  render(
    <ThemeProvider>
      <ThemeControls />
    </ThemeProvider>,
  );

  fireEvent.click(screen.getByRole("button", { name: "Use dark" }));

  expect(document.documentElement.dataset.theme).toBe("dark");
  expect(storage.getItem("agent-hub.theme-preference")).toBe("dark");
});

it("the system choice changes with the operating system", () => {
  storage.setItem("agent-hub.theme-preference", "system");
  render(
    <ThemeProvider>
      <ThemeControls />
    </ThemeProvider>,
  );

  act(() => changeListener?.({ matches: true } as MediaQueryListEvent));

  expect(document.documentElement.dataset.theme).toBe("dark");
});
