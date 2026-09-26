import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AppEnvironmentVariablesPanel } from "@/features/apps/app-environment-variables-panel";

const mocks = vi.hoisted(() => ({
  data: null as null | {
    variables: Array<Record<string, unknown>>;
    can_manage: boolean;
    pagination: { next_cursor: string | null };
  },
  create: vi.fn(),
  update: vi.fn(),
  remove: vi.fn(),
}));

vi.mock("@/api/queries", () => ({
  useAppEnvironmentVariables: () => ({
    data: mocks.data,
    error: null,
    isError: false,
    isLoading: false,
    isFetching: false,
    refetch: vi.fn(),
  }),
}));

vi.mock("@/api/mutations", () => ({
  useCreateAppEnvironmentVariable: () => ({
    isPending: false,
    mutateAsync: mocks.create,
  }),
  useUpdateAppEnvironmentVariable: () => ({
    isPending: false,
    mutateAsync: mocks.update,
  }),
  useDeleteAppEnvironmentVariable: () => ({
    isPending: false,
    mutateAsync: mocks.remove,
  }),
}));

vi.mock("@/hooks/use-cursor-pagination", () => ({
  useCursorPagination: () => ({
    cursor: undefined,
    goFirst: vi.fn(),
    goNext: vi.fn(),
    goPrevious: vi.fn(),
    canFirst: false,
    canNext: false,
    canPrevious: false,
  }),
}));

function configuredSecret() {
  return {
    id: "variable-1",
    key: "PAYMENT_TOKEN",
    is_secret: true,
    has_value: true,
    description: "Used by the payment client",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

function configuredVariable() {
  return {
    id: "variable-2",
    key: "PUBLIC_API_ORIGIN",
    is_secret: false,
    has_value: true,
    description: "Public service URL",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

describe("AppEnvironmentVariablesPanel", () => {
  beforeEach(() => {
    mocks.data = {
      variables: [configuredSecret(), configuredVariable()],
      can_manage: true,
      pagination: { next_cursor: null },
    };
    mocks.create.mockReset().mockResolvedValue(undefined);
    mocks.update.mockReset().mockResolvedValue(undefined);
    mocks.remove.mockReset().mockResolvedValue(undefined);
  });

  it("shows variable metadata and a hidden configured value without revealing its contents", () => {
    render(
      <AppEnvironmentVariablesPanel projectId="project-1" appId="app-1" />,
    );

    expect(screen.getByText("PAYMENT_TOKEN")).toBeInTheDocument();
    expect(screen.getByText("PUBLIC_API_ORIGIN")).toBeInTheDocument();
    expect(screen.getByText("Secret")).toBeInTheDocument();
    expect(screen.getByText("Variable")).toBeInTheDocument();
    expect(screen.getAllByText("Configured · hidden")).toHaveLength(2);
    expect(screen.queryByText(/secret-value|ciphertext/i)).toBeNull();
  });

  it("keeps metadata readable while hiding mutation controls for read-only users", () => {
    mocks.data = {
      variables: [configuredSecret(), configuredVariable()],
      can_manage: false,
      pagination: { next_cursor: null },
    };
    render(
      <AppEnvironmentVariablesPanel projectId="project-1" appId="app-1" />,
    );

    expect(screen.getByText("PAYMENT_TOKEN")).toBeInTheDocument();
    expect(screen.getByText("Read-only")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add variable" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Replace value" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Delete" })).toBeNull();
  });

  it("submits a secret as write-only input and does not render the submitted value", async () => {
    mocks.data = {
      variables: [],
      can_manage: true,
      pagination: { next_cursor: null },
    };
    render(
      <AppEnvironmentVariablesPanel projectId="project-1" appId="app-1" />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Add variable" }));
    const dialog = screen.getByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("Key"), {
      target: { value: "PAYMENT_TOKEN" },
    });
    fireEvent.change(within(dialog).getByLabelText("Value"), {
      target: { value: "fake-smoke-secret-value" },
    });
    fireEvent.change(within(dialog).getByLabelText("Type"), {
      target: { value: "secret" },
    });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Add variable" }),
    );

    await waitFor(() =>
      expect(mocks.create).toHaveBeenCalledWith({
        key: "PAYMENT_TOKEN",
        value: "fake-smoke-secret-value",
        is_secret: true,
      }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.queryByText("fake-smoke-secret-value")).toBeNull();
  });
});
