"use client";
import { type ColumnDef } from "@tanstack/react-table";
import { useState } from "react";
import { toast } from "sonner";
import { useCreateProjectUser } from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { useProjectUsers } from "@/api/queries";
import type { ProjectUser } from "@/api/types";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { pageControls } from "@/lib/pagination";

export function UsersView({ projectId }: { projectId: string }) {
  const navigation = useCursorPagination();
  const [createOpen, setCreateOpen] = useState(false);
  const query = useProjectUsers(projectId, { cursor: navigation.cursor });
  const create = useCreateProjectUser(projectId);
  const handleCreateUser = async (values: Record<string, string>) => {
    await create.mutateAsync({
      email: values.email,
      password: values.password,
      name: values.name || null,
    });
    toast.success("User created");
  };
  const columns: ColumnDef<ProjectUser, unknown>[] = [
    {
      accessorKey: "email",
      header: "Identity",
      cell: ({ row }) => (
        <div>
          <p className="font-medium text-white">{row.original.email}</p>
          <p className="text-xs text-slate-500">
            {row.original.name ?? "Unnamed user"}
          </p>
        </div>
      ),
    },
    {
      accessorKey: "status",
      header: "Status",
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: "email_verified",
      header: "Verified",
      cell: ({ row }) =>
        row.original.email_verified ? (
          <Badge variant="success">Verified</Badge>
        ) : (
          <Badge variant="warning">Pending</Badge>
        ),
    },
    {
      accessorKey: "created_at",
      header: "Created",
      cell: ({ row }) => formatDate(row.original.created_at),
    },
  ];
  return (
    <>
      <PageHeader
        eyebrow="Auth"
        title="Users"
        description="Application users for this project. Console account sessions and project users are separate domains."
        actions={
          <CreateDialog
            open={createOpen}
            onOpenChange={setCreateOpen}
            title="Create an application user"
            description="The password is accepted by the API and is not stored in the console."
            fields={[
              { name: "email", label: "Email", type: "email" },
              {
                name: "password",
                label: "Temporary password",
                type: "password",
                help: "Minimum 12 characters.",
              },
              { name: "name", label: "Name", required: false },
            ]}
            pending={create.isPending}
            onSubmit={handleCreateUser}
          />
        }
      />
      {query.isError ? (
        <ErrorState error={query.error} retry={() => query.refetch()} />
      ) : query.data?.users.length ? (
        <Card>
          <DataTable
            data={query.data.users}
            columns={columns}
            serverPagination={pageControls(
              navigation,
              nextCursor(query.data),
              query.isFetching,
            )}
          />
        </Card>
      ) : query.isLoading ? (
        <Card>
          <DataTable data={[]} columns={columns} loading />
        </Card>
      ) : (
        <EmptyState
          title="No application users yet"
          description="Create an application identity to test authentication and user-scoped data."
          actionLabel="Create user"
          action={() => setCreateOpen(true)}
        />
      )}
    </>
  );
}
