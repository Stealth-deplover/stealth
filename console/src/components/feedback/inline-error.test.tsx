import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { InlineError } from "./inline-error";

describe("InlineError", () => {
  it("announces the contextual recovery message", () => {
    render(<InlineError>Telemetry sources are unavailable.</InlineError>);

    expect(screen.getByRole("alert")).toHaveTextContent(
      "Telemetry sources are unavailable.",
    );
    expect(screen.getByRole("alert")).toHaveAttribute("aria-atomic", "true");
  });
});
