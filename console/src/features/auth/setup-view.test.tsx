import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { SetupView } from "@/features/auth/setup-view";

describe("legacy setup view", () => {
  it("routes consumers to the browser wizard", () => {
    render(<SetupView />);

    expect(
      screen.getByRole("link", { name: /open browser setup/i }),
    ).toHaveAttribute("href", "/setup");
  });

  it("does not expose a Device Flow onboarding control", () => {
    render(<SetupView />);

    expect(screen.queryByRole("button", { name: /device|github/i })).toBeNull();
    expect(screen.queryByText(/device code/i)).toBeNull();
  });
});
