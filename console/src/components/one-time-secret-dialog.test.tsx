import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  maskSecret,
  OneTimeSecretDialog,
} from "@/components/one-time-secret-dialog";

describe("one-time secret dialog", () => {
  it("masks the value until the user reveals it", () => {
    expect(maskSecret("sk_live_123456789")).not.toContain("123456789");

    render(<OneTimeSecretDialog secret="sk_live_123456789" onDone={vi.fn()} />);
    expect(screen.getByTestId("one-time-secret")).not.toHaveTextContent(
      "sk_live_123456789",
    );

    fireEvent.click(screen.getByRole("button", { name: "Reveal" }));
    expect(screen.getByTestId("one-time-secret")).toHaveTextContent(
      "sk_live_123456789",
    );
  });

  it("requires an explicit acknowledgment before clearing the secret", () => {
    const onDone = vi.fn();
    render(<OneTimeSecretDialog secret="webhook-secret" onDone={onDone} />);

    expect(screen.getByRole("button", { name: "Done" })).toBeDisabled();
    expect(
      screen.queryByRole("button", { name: "Close dialog" }),
    ).not.toBeInTheDocument();

    fireEvent.click(
      screen.getByRole("checkbox", { name: /saved this secret/i }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(onDone).toHaveBeenCalledOnce();
  });

  it("uses the browser clipboard without persisting the value", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    render(<OneTimeSecretDialog secret="copy-me" onDone={vi.fn()} />);

    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    await Promise.resolve();
    expect(writeText).toHaveBeenCalledWith("copy-me");
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
  });
});
