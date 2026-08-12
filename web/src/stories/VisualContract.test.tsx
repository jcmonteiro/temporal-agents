// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

describe("visual contract", () => {
  it("demonstrates the shared control, feedback, and status behavior", async () => {
    const { VisualContract } = await import("./VisualContract");

    render(<VisualContract />);

    expect(screen.getByRole("heading", { level: 1, name: "Quiet focus for active work" })).toBeTruthy();
    expect((screen.getByLabelText("Run name") as HTMLInputElement).value).toBe("Checkout reliability review");
    expect((screen.getByRole("button", { name: "Start review" }) as HTMLButtonElement).disabled).toBe(false);
    expect((screen.getByRole("button", { name: "Unavailable action" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByRole("status", { name: "Success" }).textContent).toContain("Configuration saved");
    expect(screen.getByText("Waiting for input")).toBeTruthy();

    const density = screen.getByRole("button", { name: "Compact density" });
    expect(density.getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(density);
    expect(density.getAttribute("aria-pressed")).toBe("true");
  });
});
