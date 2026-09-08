"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";
import { UpdateProjectUserStatusRequestStatus } from "@/api/generated/schema";
import {
  useDeleteProjectUser,
  useUpdateProjectUserStatus,
} from "@/api/mutations";
import { useProjectUser, useProjectUsers } from "@/api/queries";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
import { BackLink } from "@/features/resources/detail-shared";

export function ProjectUserDetailView({
  organizationId,
  projectId,
  userId,
}: {
  organizationId: string;
  projectId: string;
  userId: string;
}) {
  const router = useRouter();
  const query = useProjectUser(projectId, userId);
  const permissions = useProjectUsers(projectId);
  const updateStatus = useUpdateProjectUserStatus(projectId, userId);
  const remove = useDeleteProjectUser(projectId, userId);
  const [actionError, setActionError] = useState<unknown>();
  const user = query.data?.user;
  const canManage = permissions.data?.can_manage === true;
  const base = "/organizations/" + organizationId + "/projects/" + projectId;

  if (query.isPending) return <LoadingState />;
  if (query.error && !user)
    return (
      <ErrorState
        title="Could not load user"
        error={query.error}
        retry={() => query.refetch()}
      />
    );
  if (!user)
    return (
      <EmptyState
        title="User not found"
        description="This application identity may have been deleted or is outside this project."
      />
    );

  const changeStatus = () => {
    const nextStatus: UpdateProjectUserStatusRequestStatus =
      user.status === "active"
        ? UpdateProjectUserStatusRequestStatus.blocked
        : UpdateProjectUserStatusRequestStatus.active;
    setActionError(undefined);
    return updateStatus
      .mutateAsync({ status: nextStatus })
      .then(() => {
        toast.success(
          nextStatus === "blocked" ? "User disabled" : "User enabled",
        );
      })
      .catch((error) => {
        setActionError(error);
        throw error;
      });
  };

  const deleteUser = async () => {
    await remove.mutateAsync();
    toast.success("User deleted");
    router.replace(base + "/users");
  };

  return (
    <>
      <BackLink href={base + "/users"} label="Back to users" />
      <PageHeader
        eyebrow="Project Auth"
        title={user.email}
        description="Application identity metadata and lifecycle state from the Go API."
      />
      <div className="mb-5 flex flex-wrap items-center gap-3 text-xs text-slate-500">
        <ResourceId id={user.id} label="User ID" />
        <StatusBadge status={user.status} />
        <span>Created {formatDate(user.created_at)}</span>
        <span>Updated {formatDate(user.updated_at)}</span>
      </div>
      {query.error ? (
        <ErrorState
          title="Could not refresh user"
          error={query.error}
          retry={() => query.refetch()}
        />
      ) : null}
      {permissions.error ? (
        <ErrorState
          title="Could not load user permissions"
          error={permissions.error}
          retry={() => permissions.refetch()}
        />
      ) : null}
      {actionError ? (
        <ErrorState title="Could not update user" error={actionError} />
      ) : null}
      <div className="grid gap-5 lg:grid-cols-[1.15fr_.85fr]">
        <Card>
          <CardHeader>
            <CardTitle>Profile</CardTitle>
          </CardHeader>
          <CardContent>
            <dl className="grid gap-5 sm:grid-cols-2">
              <div>
                <dt className="text-xs text-slate-500">Email</dt>
                <dd className="mt-1 break-all text-sm text-white">
                  {user.email}
                </dd>
              </div>
              <div>
                <dt className="text-xs text-slate-500">Name</dt>
                <dd className="mt-1 text-sm text-white">
                  {user.name ?? "Unnamed user"}
                </dd>
              </div>
              <div>
                <dt className="text-xs text-slate-500">Email verification</dt>
                <dd className="mt-1">
                  <Badge variant={user.email_verified ? "success" : "warning"}>
                    {user.email_verified ? "Verified" : "Pending"}
                  </Badge>
                </dd>
              </div>
              <div>
                <dt className="text-xs text-slate-500">Status</dt>
                <dd className="mt-1">
                  <StatusBadge status={user.status} />
                </dd>
              </div>
            </dl>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Metadata</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4 text-sm">
            <div>
              <p className="text-xs text-slate-500">User ID</p>
              <ResourceId id={user.id} label="User ID" />
            </div>
            <div>
              <p className="text-xs text-slate-500">Project ID</p>
              <ResourceId id={user.project_id} label="Project ID" />
            </div>
            <div>
              <p className="text-xs text-slate-500">Created</p>
              <p className="mt-1 text-slate-300">
                {formatDate(user.created_at)}
              </p>
            </div>
            <div>
              <p className="text-xs text-slate-500">Updated</p>
              <p className="mt-1 text-slate-300">
                {formatDate(user.updated_at)}
              </p>
            </div>
          </CardContent>
        </Card>
      </div>
      <Card className="mt-5">
        <CardHeader>
          <CardTitle>Security actions</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          {canManage ? (
            <>
              <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-stealth-border p-4">
                <div>
                  <p className="text-sm font-medium text-white">
                    {user.status === "active" ? "Disable user" : "Enable user"}
                  </p>
                  <p className="mt-1 text-xs leading-5 text-slate-500">
                    {user.status === "active"
                      ? "Blocked users cannot sign in to the project application."
                      : "Allow this application identity to sign in again."}
                  </p>
                </div>
                {user.status === "active" ? (
                  <ConfirmDialog
                    trigger={
                      <Button
                        variant="outline"
                        disabled={updateStatus.isPending}
                      >
                        Disable user
                      </Button>
                    }
                    title="Disable user?"
                    description="This blocks the application identity from signing in until it is enabled again."
                    confirmLabel="Disable user"
                    pending={updateStatus.isPending}
                    onConfirm={changeStatus}
                  />
                ) : (
                  <Button
                    variant="outline"
                    disabled={updateStatus.isPending}
                    onClick={() => {
                      void changeStatus().catch(() => undefined);
                    }}
                  >
                    {updateStatus.isPending ? "Updating…" : "Enable user"}
                  </Button>
                )}
              </div>
              <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-rose-300/20 p-4">
                <div>
                  <p className="text-sm font-medium text-white">Delete user</p>
                  <p className="mt-1 text-xs leading-5 text-slate-500">
                    Permanently revokes this user&apos;s sessions and recovery
                    tokens.
                  </p>
                </div>
                <ConfirmDialog
                  trigger={
                    <Button variant="destructive" disabled={remove.isPending}>
                      Delete user
                    </Button>
                  }
                  title="Delete user?"
                  description="This permanently removes this user from the project and revokes its sessions and recovery tokens."
                  confirmLabel="Delete user"
                  pending={remove.isPending}
                  onConfirm={deleteUser}
                />
              </div>
            </>
          ) : (
            <p className="text-sm text-slate-500">
              This project membership is read-only for user lifecycle actions.
            </p>
          )}
          <p className="text-xs leading-5 text-slate-600">
            Project user sessions and activity endpoints are not exposed by the
            current API.
          </p>
        </CardContent>
      </Card>
      <Button asChild variant="ghost" className="mt-5">
        <Link href={base + "/users"}>Return to users</Link>
      </Button>
    </>
  );
}
