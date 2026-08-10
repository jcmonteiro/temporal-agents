// Browser APIs that jsdom does not implement but the Overview needs. Both are
// pure environment gaps, not behaviour under test.
if (typeof window !== "undefined") {
  if (!("ResizeObserver" in window)) {
    class ResizeObserverStub {
      observe(): void {}
      unobserve(): void {}
      disconnect(): void {}
    }
    Object.defineProperty(window, "ResizeObserver", {
      value: ResizeObserverStub,
      writable: true,
    });
  }

  if (!window.matchMedia) {
    Object.defineProperty(window, "matchMedia", {
      value: (query: string) => ({
        matches: false,
        media: query,
        addEventListener: () => {},
        removeEventListener: () => {},
      }),
      writable: true,
    });
  }
}
