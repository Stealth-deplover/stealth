import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  BrowserSetupField,
  submitGitHubManifest,
} from "@/features/auth/browser-setup-view";

describe("GitHub App Manifest submission", () => {
  afterEach(() => {
    document.body.replaceChildren();
    vi.restoreAllMocks();
  });

  it("posts the non-secret manifest payload to GitHub", () => {
    const submit = vi
      .spyOn(HTMLFormElement.prototype, "submit")
      .mockImplementation(() => undefined);

    submitGitHubManifest(
      "https://github.com/settings/apps/new?state=short-lived-state",
      '{"name":"Stealth Setup"}',
    );

    const form = document.querySelector("form");
    const field = form?.querySelector<HTMLInputElement>(
      'input[name="manifest"]',
    );
    expect(form).toHaveAttribute("method", "post");
    expect(form).toHaveAttribute(
      "action",
      "https://github.com/settings/apps/new?state=short-lived-state",
    );
    expect(field?.value).toBe('{"name":"Stealth Setup"}');
    expect(submit).toHaveBeenCalledOnce();
  });

  it("rejects a non-GitHub destination", () => {
    expect(() =>
      submitGitHubManifest("https://example.test/settings/apps/new", "{}"),
    ).toThrow(/invalid App Manifest destination/i);
  });
});

describe("BrowserSetupField", () => {
  it("associates an invalid control with its field error", () => {
    render(
      <BrowserSetupField
        id="setup-client-id"
        label="Client ID"
        hint="Use the identifier from GitHub."
        error="Client ID is required."
      >
        <input id="setup-client-id" />
      </BrowserSetupField>,
    );

    const input = screen.getByRole("textbox", { name: "Client ID" });
    const error = screen.getByRole("alert");

    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(input).toHaveAttribute(
      "aria-describedby",
      "setup-client-id-hint setup-client-id-error",
    );
    expect(error).toHaveAttribute("id", "setup-client-id-error");
    expect(error).toHaveTextContent("Client ID is required.");
  });
});
