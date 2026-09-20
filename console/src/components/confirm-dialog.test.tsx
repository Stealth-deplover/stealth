import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ConfirmDialog } from "@/components/confirm-dialog";

describe("ConfirmDialog", () => {
  it("announces an async confirmation failure", async () => {
    const onConfirm = vi
      .fn()
      .mockRejectedValue(new Error("The action could not be completed."));

    render(
      <ConfirmDialog
        trigger={<button type="button">Delete project</button>}
        title="Delete project"
        description="This cannot be undone."
        onConfirm={onConfirm}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Delete project" }));
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));

    await waitFor(() => {
      expect(screen.getByRole("alert")).toHaveTextContent(
        "The action could not be completed.",
      );
    });
  });
});
