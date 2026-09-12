"use client";
import Link from "next/link";
import { useRouter } from "next/navigation";
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
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { pageControls } from "@/lib/pagination";
import {
  type UserFormValues,
  userFields,
  userPayload,
} from "@/features/users/user-form";

export function UsersView({
  organizationId,
  projectId,
}: {
  organizationId: string;
  projectId: string;
}) {
  const router = useRouter();
  const navigation = useCursorPagination();
  const [createOpen, setCreateOpen] = useState(false);
  const query = useProjectUsers(projectId, { cursor: navigation.cursor });
  const create = useCreateProjectUser(projectId);
  const users = query.data?.users ?? [];
  const handleCreateUser = async (values: UserFormValues) => {
    const result = await create.mutateAsync(userPayload(values));
    toast.success("User created");
    if (result?.user.id)
      router.push(
        "/organizations/" +
          organizationId +
          "/projects/" +
          projectId +
          "/users/" +
          result.user.id,
      );
  };
  const canManage = query.data?.can_manage === true;
  const columns: ColumnDef<ProjectUser, unknown>[] = [
    {
      accessorKey: "email",
      header: "Identity",
      cell: ({ row }) => (
        <div>
          <Link
            href={
              "/organizations/" +
              organizationId +
              "/projects/" +
              projectId +
              "/users/" +
              row.original.id
            }
            className="font-medium text-white hover:text-cyan-200"
          >
            {row.original.email}
          </Link>
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
    {
      id: "actions",
      header: "",
      cell: ({ row }) => (
        <Button asChild variant="ghost" size="sm">
          <Link
            href={
              "/organizations/" +
              organizationId +
              "/projects/" +
              projectId +
              "/users/" +
              row.original.id
            }
          >
            Open user
          </Link>
        </Button>
      ),
    },
  ];
  return (
    <>
      <PageHeader
        eyebrow="Auth"
        title="Users"
        description="Application users for this project. Console account sessions and project users are separate domains."
        actions={
          canManage ? (
            <CreateDialog<UserFormValues>
              open={createOpen}
              onOpenChange={setCreateOpen}
              triggerLabel="Create user"
              submitLabel="Create user"
              pendingLabel="Creating user…"
              title="Create an application user"
              description="The password is accepted by the API and is not stored in the console."
              fields={userFields}
              pending={create.isPending}
              onSubmit={handleCreateUser}
            />
          ) : null
        }
      />
      {query.isError ? (
        <ErrorState
          title="Could not load application users"
          error={query.error}
          retry={() => query.refetch()}
        />
      ) : query.isPending ? (
        <Card>
          <DataTable data={[]} columns={columns} loading />
        </Card>
      ) : users.length || navigation.canFirst || nextCursor(query.data) ? (
        <Card>
          <DataTable
            data={users}
            columns={columns}
            serverPagination={pageControls(
              navigation,
              nextCursor(query.data),
              query.isFetching,
            )}
          />
        </Card>
      ) : (
        <EmptyState
          title="No application users yet"
          description={
            canManage
              ? "Create an application identity to test authentication and user-scoped data."
              : "No application identities are visible in this project."
          }
          actionLabel={canManage ? "Create user" : undefined}
          action={canManage ? () => setCreateOpen(true) : undefined}
        />
      )}
    </>
  );
}
