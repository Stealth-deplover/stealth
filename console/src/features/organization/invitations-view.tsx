"use client";

import type { ColumnDef } from "@tanstack/react-table";
import { useState } from "react";
import { toast } from "sonner";
import {
  CreateOrganizationInvitationRequestRole,
  OrganizationInvitationResponseDelivery,
} from "@/api/generated/schema";
import {
  useCreateOrganizationInvitation,
  useRevokeOrganizationInvitation,
} from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { useOrganizationInvitations } from "@/api/queries";
import type { OrganizationInvitation } from "@/api/types";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { formatDate } from "@/lib/format";
import { pageControls } from "@/lib/pagination";

const roleOptions = Object.values(CreateOrganizationInvitationRequestRole).map(
  (role) => ({
    value: role,
    label: role[0].toUpperCase() + role.slice(1),
  }),
);

export function OrganizationInvitationsView({
  organizationId,
}: {
  organizationId: string;
}) {
  const navigation = useCursorPagination();
  const query = useOrganizationInvitations(organizationId, {
    cursor: navigation.cursor,
  });
  const create = useCreateOrganizationInvitation(organizationId);
  const revoke = useRevokeOrganizationInvitation(organizationId);
  const [createOpen, setCreateOpen] = useState(false);
  const [created, setCreated] = useState<OrganizationInvitation>();
  const [createdDelivery, setCreatedDelivery] =
    useState<OrganizationInvitationResponseDelivery>();
  const canManage = query.data?.can_manage === true;
  const invitations = query.data?.invitations ?? [];
  const handleCreate = async (values: Record<string, string>) => {
    const result = await create.mutateAsync({
      email: values.email,
      role: values.role as CreateOrganizationInvitationRequestRole,
    });
    setCreated(result?.invitation);
    setCreatedDelivery(result?.delivery);
    toast.success(
      result?.delivery === "sent"
        ? "Invitation created; delivery accepted"
        : "Invitation created; email delivery failed",
    );
  };
  const columns: ColumnDef<OrganizationInvitation, unknown>[] = [
    {
      accessorKey: "email",
      header: "Email",
      cell: ({ row }) => (
        <span className="font-medium text-white">{row.original.email}</span>
      ),
    },
    {
      accessorKey: "role",
      header: "Role",
      cell: ({ row }) => <Badge variant="neutral">{row.original.role}</Badge>,
    },
    {
      accessorKey: "status",
      header: "Status",
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: "created_at",
      header: "Created",
      cell: ({ row }) => formatDate(row.original.created_at),
    },
    {
      accessorKey: "expires_at",
      header: "Expires",
      cell: ({ row }) => formatDate(row.original.expires_at),
    },
    {
      id: "actions",
      header: "",
      cell: ({ row }) =>
        canManage ? (
          <ConfirmDialog
            trigger={
              <Button variant="ghost" size="sm" className="text-rose-300">
                Revoke
              </Button>
            }
            title="Revoke invitation?"
            description={
              "This invalidates the invitation for " +
              row.original.email +
              ". The recipient must use a new invitation."
            }
            confirmLabel="Revoke invitation"
            pending={revoke.isPending}
            onConfirm={async () => {
              await revoke.mutateAsync(row.original.id);
              toast.success("Invitation revoked");
            }}
          />
        ) : (
          <span className="text-xs text-slate-600">Read-only</span>
        ),
    },
  ];

  return (
    <>
      <PageHeader
        eyebrow="Organization"
        title="Invitations"
        description="Create and revoke email-bound invitations. The API keeps the one-time token out of the Console response."
        actions={
          canManage ? (
            <CreateDialog
              open={createOpen}
              onOpenChange={setCreateOpen}
              triggerLabel="Invite member"
              submitLabel="Create invitation"
              pendingLabel="Creating invitation…"
              title="Invite a member"
              description="The invitation record is persisted by the Go API. Email delivery is reported separately."
              fields={[
                { name: "email", label: "Email", type: "email" },
                {
                  name: "role",
                  label: "Role",
                  type: "select",
                  defaultValue: "developer",
                  options: roleOptions,
                },
              ]}
              pending={create.isPending}
              onSubmit={handleCreate}
            />
          ) : null
        }
      />
      {created ? (
        <div
          role="status"
          className="mb-5 rounded-lg border border-cyan-300/20 bg-cyan-300/[0.06] px-4 py-3 text-sm text-cyan-100"
        >
          Invitation created for {created.email}.{" "}
          {createdDelivery === OrganizationInvitationResponseDelivery.sent
            ? "The configured mailer accepted delivery; the one-time token is not exposed here."
            : "The invitation record remains available, but the configured mailer did not accept delivery."}
        </div>
      ) : null}
      {query.isError ? (
        <ErrorState
          title="Could not load organization invitations"
          error={query.error}
          retry={() => query.refetch()}
        />
      ) : query.isPending ? (
        <Card>
          <DataTable data={[]} columns={columns} loading />
        </Card>
      ) : invitations.length ||
        navigation.canFirst ||
        nextCursor(query.data) ? (
        <Card>
          <DataTable
            columns={columns}
            data={invitations}
            empty="No invitations on this page."
            serverPagination={pageControls(
              navigation,
              nextCursor(query.data),
              query.isFetching,
            )}
          />
        </Card>
      ) : (
        <EmptyState
          title="No pending invitations"
          description="Invite teammates to collaborate on this organization."
          actionLabel={canManage ? "Invite member" : undefined}
          action={canManage ? () => setCreateOpen(true) : undefined}
        />
      )}
    </>
  );
}
