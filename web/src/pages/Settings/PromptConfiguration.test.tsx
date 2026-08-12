// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { fireEvent } from "@testing-library/dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { PromptConfiguration } from "./PromptConfiguration";
import { aDirectoryPlace, aPrompt, FakeApi } from "../../test/fake-api";

let api: FakeApi;

beforeEach(() => {
  api = new FakeApi();
  api.install();
  vi.spyOn(window, "confirm").mockReturnValue(true);
});

afterEach(() => {
  cleanup();
  api.restore();
  vi.restoreAllMocks();
});

async function openConfiguration(
  fixedLocation?: { id: string; label: string },
): Promise<void> {
  render(<PromptConfiguration fixedLocation={fixedLocation} />);
  await waitFor(() => expect(screen.queryByText("Reading the instructions…")).toBeNull());
}

it("renders inherited and overridden state exactly as the server reports it", async () => {
  api.promptCatalogues["place-1"] = [
    aPrompt({
      key: "review.perform",
      effective: "Review for the parent repository",
      inherited: "Review for the parent repository",
      source: "directory",
      inheritedFrom: "directory",
      overridden: false,
    }),
    aPrompt({
      key: "review.implement",
      effective: "Implement here {{.Review}}",
      inherited: "Implement globally {{.Review}}",
      source: "directory",
      inheritedFrom: "global",
      overridden: true,
    }),
  ];

  await openConfiguration({ id: "place-1", label: "feature" });

  expect(screen.getByRole("button", { name: /review\.perform inherited · directory/i })).toBeTruthy();
  expect(screen.getByRole("button", { name: /review\.implement overridden here/i })).toBeTruthy();
  expect((screen.getByLabelText("Instruction text") as HTMLTextAreaElement).value).toBe(
    "Review for the parent repository",
  );
});

it("selects a registered place from the configuration destination", async () => {
  api.locations.push(aDirectoryPlace({ id: "place-1", label: "pricing" }));
  api.registered = [{ locationId: "place-1", registeredAt: null }];
  api.promptCatalogues.global = [aPrompt({ effective: "Review globally" })];
  api.promptCatalogues["place-1"] = [aPrompt({ effective: "Review pricing" })];
  await openConfiguration();
  await waitFor(() =>
    expect(screen.getByRole("option", { name: "pricing" })).toBeTruthy(),
  );

  fireEvent.change(screen.getByLabelText("Configuration scope"), {
    target: { value: "place-1" },
  });

  await waitFor(() =>
    expect((screen.getByLabelText("Instruction text") as HTMLTextAreaElement).value).toBe(
      "Review pricing",
    ),
  );
});

it("shows a diff against the server-provided inherited value before saving", async () => {
  api.promptCatalogues.global = [
    aPrompt({
      effective: "Review the branch",
      inherited: "Review the shipped branch",
      overridden: true,
    }),
  ];
  await openConfiguration();

  fireEvent.change(screen.getByLabelText("Instruction text"), {
    target: { value: "Review the branch and its tests" },
  });

  const diff = screen.getByRole("region", { name: "Changes against inherited value" });
  expect(diff.textContent).toContain("- Review the shipped branch");
  expect(diff.textContent).toContain("+ Review the branch and its tests");
  expect(screen.getByText(/applies to every place that inherits/i)).toBeTruthy();
});

it("confirms that an instruction override was saved", async () => {
  api.promptCatalogues.global = [aPrompt({ effective: "Review the branch" })];
  await openConfiguration();
  fireEvent.change(screen.getByLabelText("Instruction text"), {
    target: { value: "Review the branch and its tests" },
  });

  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Save override" }));
  });

  expect(screen.getByRole("status").textContent).toMatch(/override saved/i);
});

it("shows a validation refusal against the instruction field", async () => {
  api.promptCatalogues.global = [
    aPrompt({
      key: "review.implement",
      effective: "Implement {{.Review}}",
      inherited: "Implement {{.Review}}",
      requiredInserts: [
        { name: "Review", action: "{{.Review}}", purpose: "The review output." },
      ],
    }),
  ];
  api.promptRefusal = "the review.implement instruction must insert {{.Review}}";
  await openConfiguration();
  fireEvent.change(screen.getByLabelText("Instruction text"), {
    target: { value: "Implement it" },
  });

  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Save override" }));
  });

  expect(screen.getByRole("alert").textContent).toContain("{{.Review}}");
  expect(screen.getByLabelText("Instruction text").getAttribute("aria-invalid")).toBe("true");
});

it("resets a place to inherited configuration with confirmation", async () => {
  api.promptCatalogues["place-1"] = [aPrompt({ overridden: true })];
  await openConfiguration({ id: "place-1", label: "feature" });

  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Return to inherited" }));
  });

  expect(window.confirm).toHaveBeenCalled();
  expect(api.promptResets).toContainEqual({ locationId: "place-1", key: "review.perform" });
});

it("resets global configuration to the shipped default with confirmation", async () => {
  api.promptCatalogues.global = [aPrompt({ overridden: true })];
  await openConfiguration();

  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Return to shipped default" }));
  });

  expect(window.confirm).toHaveBeenCalled();
  expect(api.promptResets).toContainEqual({ locationId: "", key: "review.perform" });
});
