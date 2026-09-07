import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { CreateDialog } from "@/components/create-dialog";

const fields = [{ name: "name", label: "Name", required: false }];

describe("CreateDialog", () => {
  it("uses explicit trigger, submit, and pending labels", () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const { rerender } = render(
      <CreateDialog
        triggerLabel="Create function"
        submitLabel="Create function"
        pendingLabel="Creating function…"
        title="Create a function"
        description="Run backend code on demand."
        fields={fields}
        onSubmit={onSubmit}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Create function" }));
    const dialog = screen.getByRole("dialog");
    const submit = within(dialog).getByRole("button", {
      name: "Create function",
    });

    expect(submit).toBeEnabled();
    rerender(
      <CreateDialog
        triggerLabel="Create function"
        submitLabel="Create function"
        pendingLabel="Creating function…"
        title="Create a function"
        description="Run backend code on demand."
        fields={fields}
        onSubmit={onSubmit}
        pending
      />,
    );
    expect(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Creating function…",
      }),
    ).toBeDisabled();
  });

  it("supports controlled opening and closing", async () => {
    function ControlledCreateDialog() {
      const [open, setOpen] = useState(false);

      return (
        <>
          <button type="button" onClick={() => setOpen(true)}>
            Open create flow
          </button>
          <CreateDialog
            open={open}
            onOpenChange={setOpen}
            triggerLabel="Create project"
            submitLabel="Create project"
            pendingLabel="Creating project…"
            title="Create a project"
            description="Create a project boundary."
            fields={fields}
            onSubmit={vi.fn()}
          />
        </>
      );
    }

    render(<ControlledCreateDialog />);
    fireEvent.click(screen.getByRole("button", { name: "Open create flow" }));

    const dialog = screen.getByRole("dialog");
    expect(dialog).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });

  it("resets values after an uncontrolled dialog is cancelled", async () => {
    render(
      <CreateDialog
        triggerLabel="Create function"
        submitLabel="Create function"
        pendingLabel="Creating function…"
        title="Create a function"
        description="Run backend code on demand."
        fields={fields}
        onSubmit={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Create function" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Name" }), {
      target: { value: "temporary" },
    });
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Cancel",
      }),
    );

    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    fireEvent.click(screen.getByRole("button", { name: "Create function" }));

    expect(screen.getByRole("textbox", { name: "Name" })).toHaveValue("");
  });

  it("resets values when a controlled parent closes the dialog", async () => {
    function ControlledCreateDialog() {
      const [open, setOpen] = useState(true);

      return (
        <>
          <button type="button" onClick={() => setOpen(false)}>
            Close from parent
          </button>
          <CreateDialog
            open={open}
            onOpenChange={setOpen}
            triggerLabel="Create project"
            submitLabel="Create project"
            pendingLabel="Creating project…"
            title="Create a project"
            description="Create a project boundary."
            fields={fields}
            onSubmit={vi.fn()}
          />
        </>
      );
    }

    render(<ControlledCreateDialog />);
    fireEvent.change(screen.getByRole("textbox", { name: "Name" }), {
      target: { value: "temporary" },
    });
    fireEvent.click(screen.getByText("Close from parent"));

    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );

    fireEvent.click(screen.getByRole("button", { name: "Create project" }));
    expect(screen.getByRole("textbox", { name: "Name" })).toHaveValue("");
  });
});
