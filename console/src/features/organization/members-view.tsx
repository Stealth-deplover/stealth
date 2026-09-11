"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import type { ColumnDef } from "@tanstack/react-table";
import { toast } from "sonner";
import {
  useDeleteOrganizationMembership,
  useUpdateOrganizationMembership,
} from "@/api/mutations";
import { UpdateOrganizationMembershipRequestRole } from "@/api/generated/schema";
import { nextCursor } from "@/api/pagination";
import { useMemberships } from "@/api/queries";
import type { Membership } from "@/api/types";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { formatDate } from "@/lib/format";
import { pageControls } from "@/lib/pagination";

const roleOptions = Object.values(UpdateOrganizationMembershipRequestRole).map(
  (role) => ({
    value: role,
    label: role[0].toUpperCase() + role.slice(1),
  }),
);

function membershipIdentity(member: Membership) {
  if (member.email) return member.email;
  if (member.provider === "github" && member.provider_login) {
    return `GitHub @${member.provider_login}`;
  }
  return "Provider identity";
}

export function OrganizationMembersView({
  organizationId,
}: {
  organizationId: string;
}) {
  const router = useRouter();
  const navigation = useCursorPagination();
  const query = useMemberships(organizationId, { cursor: navigation.cursor });
  const update = useUpdateOrganizationMembership(organizationId);
  const remove = useDeleteOrganizationMembership(organizationId);
  const canManage = query.data?.can_manage === true;
  const members = query.data?.memberships ?? [];
  const columns: ColumnDef<Membership, unknown>[] = [
    {
      accessorKey: "email",
      header: "Identity",
      cell: ({ row }) => (
        <div>
          <p className="font-medium text-white">
            {membershipIdentity(row.original)}
          </p>
          <p className="font-mono text-[10px] text-slate-600">
            {row.original.account_id}
          </p>
        </div>
      ),
    },
    {
      accessorKey: "role",
      header: "Role",
      cell: ({ row }) => (
        <Badge variant={row.original.role === "owner" ? "default" : "neutral"}>
          {row.original.role}
        </Badge>
      ),
    },
    {
      accessorKey: "created_at",
      header: "Joined",
      cell: ({ row }) => formatDate(row.original.created_at),
    },
    {
      id: "actions",
      header: "",
      cell: ({ row }) =>
        canManage && row.original.role !== "owner" ? (
          <div className="flex items-center justify-end gap-1">
            <CreateDialog
              trigger={
                <Button variant="ghost" size="sm">
                  Change role
                </Button>
              }
              triggerLabel="Change role"
              submitLabel="Save role"
              pendingLabel="Saving role…"
              title="Change member role"
              description="The Go API enforces which organization roles the current member may change."
              fields={[
                {
                  name: "role",
                  label: "Role",
                  type: "select",
                  defaultValue: row.original.role,
                  options: roleOptions,
                },
              ]}
              pending={update.isPending}
              onSubmit={async (values) => {
                await update.mutateAsync({
                  accountId: row.original.account_id,
                  body: {
                    role: values.role as UpdateOrganizationMembershipRequestRole,
                  },
                });
                toast.success("Member role updated");
              }}
            />
            <ConfirmDialog
              trigger={
                <Button variant="ghost" size="sm" className="text-rose-300">
                  Remove
                </Button>
              }
              title="Remove member?"
              description={
                "This removes " +
                membershipIdentity(row.original) +
                " from the organization. Their project access is revoked."
              }
              confirmLabel="Remove member"
              pending={remove.isPending}
              onConfirm={async () => {
                await remove.mutateAsync(row.original.account_id);
                toast.success("Member removed");
              }}
            />
          </div>
        ) : (
          <span className="text-xs text-slate-600">
            {row.original.role === "owner" ? "Owner protected" : "Read-only"}
          </span>
        ),
    },
  ];

  return (
    <>
      <PageHeader
        eyebrow="Organization"
        title="Members"
        description="Membership roles and access changes are enforced by the Go API."
        actions={
          canManage ? (
            <Button asChild variant="outline">
              <Link href={"/organizations/" + organizationId + "/invitations"}>
                Invitations
              </Link>
            </Button>
          ) : null
        }
      />
      {query.isError ? (
        <ErrorState
          title="Could not load organization members"
          error={query.error}
          retry={() => query.refetch()}
        />
      ) : query.isPending ? (
        <Card>
          <DataTable data={[]} columns={columns} loading />
        </Card>
      ) : members.length || navigation.canFirst || nextCursor(query.data) ? (
        <Card>
          <DataTable
            columns={columns}
            data={members}
            empty="No members on this page."
            serverPagination={pageControls(
              navigation,
              nextCursor(query.data),
              query.isFetching,
            )}
          />
        </Card>
      ) : (
        <EmptyState
          title="No members yet"
          description={
            canManage
              ? "Invite teammates to collaborate on this organization."
              : "No organization members are visible."
          }
          actionLabel={canManage ? "Invite member" : undefined}
          action={
            canManage
              ? () =>
                  router.push(
                    "/organizations/" + organizationId + "/invitations",
                  )
              : undefined
          }
        />
      )}
    </>
  );
}
