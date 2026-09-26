import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AppRollbackDialog } from "@/features/apps/app-rollback-dialog";

describe("AppRollbackDialog", () => {
  it("explains the restore and preservation behavior before explicit confirmation", () => {
    const onConfirm = vi.fn();
    const onOpenChange = vi.fn();
    render(
      <AppRollbackDialog
        appName="payments"
        currentVersion={5}
        targetVersion={3}
        open
        pending={false}
        onOpenChange={onOpenChange}
        onConfirm={onConfirm}
      />,
    );

    expect(
      screen.getByRole("heading", {
        name: "Roll back payments from v5 to v3?",
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Its captured runtime WorkloadSpec is restored."),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Current environment variables and secrets stay as-is."),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        /route stays withdrawn until the rolled-back runtime passes/i,
      ),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
    fireEvent.click(screen.getByRole("button", { name: "Rollback to v3" }));
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it("disables both actions while the rollback request is pending", () => {
    render(
      <AppRollbackDialog
        appName="payments"
        currentVersion={5}
        targetVersion={3}
        open
        pending
        onOpenChange={vi.fn()}
        onConfirm={vi.fn()}
      />,
    );

    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Rolling back…" }),
    ).toBeDisabled();
  });
});
