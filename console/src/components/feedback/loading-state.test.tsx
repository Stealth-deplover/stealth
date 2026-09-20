import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { LoadingState } from "./loading-state";

describe("LoadingState", () => {
  it("announces loading without exposing skeleton rows as content", () => {
    render(<LoadingState rows={2} />);

    expect(screen.getByRole("status")).toHaveAttribute("aria-live", "polite");
    expect(screen.getByRole("status")).toHaveAttribute("aria-busy", "true");
    expect(screen.getByText("Loading…")).toBeInTheDocument();
    expect(screen.queryAllByRole("status")).toHaveLength(1);
  });

  it("supports an accessible label for custom visual fallbacks", () => {
    render(
      <LoadingState label="Loading sign-in form…">
        <div data-testid="custom-placeholder" />
      </LoadingState>,
    );

    expect(screen.getByRole("status")).toHaveAttribute(
      "aria-label",
      "Loading sign-in form",
    );
    expect(screen.getByText("Loading sign-in form…")).toBeInTheDocument();
    expect(screen.getByTestId("custom-placeholder")).toBeInTheDocument();
  });
});
