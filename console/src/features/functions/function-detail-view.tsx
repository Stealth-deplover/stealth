"use client";
import Link from "next/link";
import type { ColumnDef } from "@tanstack/react-table";
import { Upload } from "lucide-react";
import { useCallback, useRef, useState } from "react";
import { toast } from "sonner";
import { api, unwrap } from "@/api/client";
import { nextCursor } from "@/api/pagination";
import {
  useActivateFunctionDeployment,
  useCreateFunctionVariable,
  useDeleteFunctionVariable,
  useUploadFunctionDeployment,
} from "@/api/mutations";
import {
  useFunction,
  useFunctionDeployment,
  useFunctionDeployments,
  useFunctionExecutions,
  useFunctionVariables,
} from "@/api/queries";
import type {
  FunctionDeployment,
  FunctionExecution,
  FunctionVariable,
} from "@/api/types";
import type { CreateFunctionVariableRequestKind } from "@/api/generated/schema";
import { DataTable } from "@/components/data-table";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { CreateDialog } from "@/components/create-dialog";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { LogViewer, type LogLine } from "@/components/log-viewer";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { formatBytes, formatDate, formatDuration } from "@/lib/format";
import {
  deploymentDurationMs,
  getDeploymentLifecycleStatus,
  isDeploymentInProgress,
} from "@/lib/deployment-state";
import { pageControls } from "@/lib/pagination";
import { BackLink } from "@/features/resources/detail-shared";

function FunctionVariablesPanel({
  projectId,
  functionId,
}: {
  projectId: string;
  functionId: string;
}) {
  const navigation = useCursorPagination("variables_cursor");
  const query = useFunctionVariables(projectId, functionId, {
    cursor: navigation.cursor,
  });
  const create = useCreateFunctionVariable(projectId, functionId);
  const remove = useDeleteFunctionVariable(projectId, functionId);
  const canManage = query.data?.can_manage === true;
  const handleCreateVariable = async (values: Record<string, string>) => {
    const kind =
      values.kind === "secret"
        ? "secret"
        : values.kind === "variable"
          ? "variable"
          : null;
    if (!kind) throw new Error("Kind must be variable or secret.");
    await create.mutateAsync({
      key: values.key,
      value: values.value,
      kind: kind as CreateFunctionVariableRequestKind,
      is_secret: kind === "secret",
      description: values.description || undefined,
    });
    toast.success("Environment variable added");
  };
  const columns: ColumnDef<FunctionVariable, unknown>[] = [
    {
      accessorKey: "key",
      header: "Key",
      cell: ({ row }) => (
        <span className="font-mono text-xs text-white">{row.original.key}</span>
      ),
    },
    {
      accessorKey: "kind",
      header: "Kind",
      cell: ({ row }) => (
        <Badge variant={row.original.is_secret ? "warning" : "neutral"}>
          {row.original.is_secret ? "Secret" : "Variable"}
        </Badge>
      ),
    },
    {
      accessorKey: "has_value",
      header: "Value",
      cell: ({ row }) => (
        <span className="text-xs text-slate-400">
          {row.original.has_value ? "Configured · hidden" : "Not set"}
        </span>
      ),
    },
    {
      accessorKey: "updated_at",
      header: "Updated",
      cell: ({ row }) => formatDate(row.original.updated_at),
    },
    {
      id: "actions",
      header: "",
      cell: ({ row }) =>
        canManage ? (
          <ConfirmDialog
            trigger={
              <Button variant="ghost" size="sm" className="text-rose-300">
                Delete
              </Button>
            }
            title="Delete this variable?"
            description={`The value for ${row.original.key} will be removed from the function configuration.`}
            confirmLabel="Delete variable"
            pending={remove.isPending}
            onConfirm={async () => {
              await remove.mutateAsync(row.original.id);
              toast.success("Variable deleted");
            }}
          />
        ) : null,
    },
  ];
  if (query.error)
    return <ErrorState error={query.error} retry={() => query.refetch()} />;
  return (
    <Card className="mt-4">
      <CardHeader className="flex-row items-center justify-between">
        <div>
          <CardTitle>Environment variables</CardTitle>
          <p className="mt-1 text-xs text-slate-500">
            Values are write-only. The Go API returns metadata and never sends
            plaintext values back to this browser.
          </p>
        </div>
        {canManage ? (
          <CreateDialog
            triggerLabel="Add variable"
            submitLabel="Add variable"
            pendingLabel="Adding variable…"
            title="Add environment variable"
            description="The value is encrypted by the Go API and cannot be recovered after submission."
            fields={[
              { name: "key", label: "Key", placeholder: "DATABASE_URL" },
              { name: "value", label: "Value", type: "password" },
              {
                name: "kind",
                label: "Kind",
                defaultValue: "variable",
                help: "Use variable or secret. Secret values are marked as sensitive.",
              },
              { name: "description", label: "Description", required: false },
            ]}
            pending={create.isPending}
            onSubmit={handleCreateVariable}
          />
        ) : query.data ? (
          <Badge variant="neutral">Read-only</Badge>
        ) : null}
      </CardHeader>
      <CardContent>
        <DataTable
          data={query.data?.variables ?? []}
          columns={columns}
          loading={query.isLoading}
          empty="No environment variables configured."
          serverPagination={pageControls(
            navigation,
            nextCursor(query.data),
            query.isFetching,
          )}
        />
      </CardContent>
    </Card>
  );
}

export function FunctionDetailView({
  organizationId,
  projectId,
  functionId,
}: {
  organizationId: string;
  projectId: string;
  functionId: string;
}) {
  const query = useFunction(projectId, functionId);
  const deploymentsNavigation = useCursorPagination("deployments_cursor");
  const executionsNavigation = useCursorPagination("executions_cursor");
  const deployments = useFunctionDeployments(projectId, functionId, {
    cursor: deploymentsNavigation.cursor,
  });
  const executions = useFunctionExecutions(projectId, functionId, {
    cursor: executionsNavigation.cursor,
  });
  const upload = useUploadFunctionDeployment(projectId, functionId);
  const activate = useActivateFunctionDeployment(projectId, functionId);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [tab, setTab] = useState("overview");
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const fn = query.data?.function;
  const canManage = deployments.data?.can_manage === true;
  const activeDeploymentId = fn?.active_deployment_id;
  const showFirstDeployment =
    !deploymentsNavigation.cursor && deployments.data?.deployments.length === 0;
  const openFilePicker = () => fileInputRef.current?.click();
  const handleDeploymentUpload = (file: File) => {
    upload.mutate(
      { file },
      {
        onSuccess: () => {
          deploymentsNavigation.goFirst();
          setTab("deployments");
          toast.success("Deployment queued");
        },
        onError: () => toast.error("Could not queue function deployment"),
      },
    );
  };
  const logs = useCallback(
    async (after?: number): Promise<LogLine[]> => {
      if (!activeDeploymentId) return [];
      const result = await api.GET(
        "/v1/projects/{projectID}/functions/{functionID}/deployments/{deploymentID}/logs",
        {
          params: {
            path: {
              projectID: projectId,
              functionID: functionId,
              deploymentID: activeDeploymentId,
            },
            query: after === undefined ? {} : { after },
          },
        },
      );
      const data = await unwrap(result);
      return (data?.logs ?? []) as LogLine[];
    },
    [activeDeploymentId, functionId, projectId],
  );
  if (query.error)
    return <ErrorState error={query.error} retry={() => query.refetch()} />;
  if (deployments.error)
    return (
      <ErrorState
        error={deployments.error}
        retry={() => deployments.refetch()}
      />
    );
  if (executions.error)
    return (
      <ErrorState error={executions.error} retry={() => executions.refetch()} />
    );
  if (!fn)
    return (
      <EmptyState
        title="Function not found"
        description="The function may have been removed or is outside this project."
      />
    );
  const deploymentColumns: ColumnDef<FunctionDeployment, unknown>[] = [
    {
      accessorKey: "version",
      header: "Version",
      cell: ({ row }) => (
        <span className="font-mono text-xs">v{row.original.version}</span>
      ),
    },
    {
      accessorKey: "status",
      header: "Status",
      cell: ({ row }) => (
        <StatusBadge status={getDeploymentLifecycleStatus(row.original)} />
      ),
    },
    {
      accessorKey: "build_status",
      header: "Build",
      cell: ({ row }) => <StatusBadge status={row.original.build_status} />,
    },
    {
      accessorKey: "source",
      header: "Source",
      cell: ({ row }) => (
        <span className="text-xs text-slate-400">
          {row.original.source_name ?? row.original.source}
        </span>
      ),
    },
    {
      accessorKey: "created_at",
      header: "Created",
      cell: ({ row }) => formatDate(row.original.created_at),
    },
    {
      id: "action",
      header: "",
      cell: ({ row }) => (
        <div className="flex items-center gap-2">
          <Link
            href={`${base}/functions/${functionId}/deployments/${row.original.id}`}
            className="text-xs text-cyan-300"
          >
            Inspect
          </Link>
          {row.original.id === activeDeploymentId ||
          row.original.status === "active" ? (
            <StatusBadge status="active" />
          ) : canManage &&
            getDeploymentLifecycleStatus(row.original) === "ready" ? (
            <Button
              size="sm"
              variant="outline"
              disabled={activate.isPending}
              onClick={() =>
                activate.mutate(row.original.id, {
                  onSuccess: () => toast.success("Deployment activated"),
                })
              }
            >
              Activate
            </Button>
          ) : null}
        </div>
      ),
    },
  ];
  const executionColumns: ColumnDef<FunctionExecution, unknown>[] = [
    {
      accessorKey: "id",
      header: "Execution",
      cell: ({ row }) => (
        <Link
          href={`${base}/functions/${functionId}/executions/${row.original.id}`}
          className="font-mono text-xs text-slate-400 hover:text-cyan-200"
          title={row.original.id}
        >
          {row.original.id.slice(0, 12)}…
        </Link>
      ),
    },
    {
      accessorKey: "status",
      header: "Status",
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      id: "duration",
      header: "Duration",
      cell: ({ row }) =>
        row.original.started_at && row.original.finished_at
          ? formatDuration(
              new Date(row.original.finished_at).valueOf() -
                new Date(row.original.started_at).valueOf(),
            )
          : "—",
    },
    {
      accessorKey: "created_at",
      header: "Created",
      cell: ({ row }) => formatDate(row.original.created_at),
    },
  ];
  return (
    <>
      <BackLink href={`${base}/functions`} label="Back to functions" />
      <PageHeader
        eyebrow="Function"
        title={fn.name}
        description="Deploy archive-based workloads and inspect builds, executions, and logs."
        actions={
          canManage ? (
            <Button
              type="button"
              disabled={upload.isPending}
              onClick={openFilePicker}
            >
              <Upload className="size-4" />
              {upload.isPending ? "Uploading…" : "Deploy function"}
            </Button>
          ) : deployments.data ? (
            <Badge variant="neutral">Read-only</Badge>
          ) : null
        }
      />
      <input
        ref={fileInputRef}
        type="file"
        accept=".zip,.tar,.gz,.tgz"
        className="sr-only"
        disabled={upload.isPending}
        aria-label="Select function deployment archive"
        onChange={(event) => {
          const file = event.target.files?.[0];
          event.currentTarget.value = "";
          if (file) handleDeploymentUpload(file);
        }}
      />
      <div className="mb-5 flex flex-wrap items-center gap-2">
        <StatusBadge status={fn.status} />
        <span className="font-mono text-xs text-slate-500">{fn.runtime}</span>
        <span className="text-xs text-slate-600">Entry {fn.entrypoint}</span>
        <span className="text-xs text-slate-600">
          Updated {formatDate(fn.updated_at)}
        </span>
        <ResourceId id={fn.id} label="Function ID" />
      </div>
      {showFirstDeployment ? (
        <Card className="mb-4 border-amber-300/20 bg-amber-300/[0.03]">
          <CardContent className="flex flex-col gap-2 p-4 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <p className="text-sm font-medium text-amber-100">
                Ready for first deployment
              </p>
              <p className="mt-1 text-xs leading-5 text-amber-200/70">
                Upload an archive to start building this function. Build logs
                will be available from the deployment detail.
              </p>
            </div>
            {canManage ? (
              <Button
                type="button"
                variant="outline"
                disabled={upload.isPending}
                onClick={openFilePicker}
              >
                <Upload className="size-4" /> Deploy function
              </Button>
            ) : null}
          </CardContent>
        </Card>
      ) : null}
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="deployments">Deployments</TabsTrigger>
          <TabsTrigger value="executions">Executions</TabsTrigger>
          <TabsTrigger value="logs">Build logs</TabsTrigger>
          <TabsTrigger value="configuration">Configuration</TabsTrigger>
        </TabsList>
        <TabsContent value="overview">
          <div className="grid gap-4 md:grid-cols-3">
            <Card>
              <CardContent className="p-5">
                <p className="text-xs text-slate-500">Runtime</p>
                <p className="mt-2 font-mono text-sm text-white">
                  {fn.runtime}
                </p>
              </CardContent>
            </Card>
            <Card>
              <CardContent className="p-5">
                <p className="text-xs text-slate-500">Active deployment</p>
                <div className="mt-1">
                  {fn.active_deployment_id ? (
                    <ResourceId
                      id={fn.active_deployment_id}
                      label="Deployment ID"
                    />
                  ) : (
                    <p className="text-sm text-slate-500">Not deployed</p>
                  )}
                </div>
              </CardContent>
            </Card>
            <Card>
              <CardContent className="p-5">
                <p className="text-xs text-slate-500">Artifact usage</p>
                <p className="mt-2 text-sm text-white">
                  {formatBytes(fn.artifact_used_bytes)} /{" "}
                  {formatBytes(fn.artifact_quota_bytes)}
                </p>
              </CardContent>
            </Card>
          </div>
          <Card className="mt-4">
            <CardHeader>
              <CardTitle>Definition</CardTitle>
            </CardHeader>
            <CardContent>
              <dl className="grid gap-4 sm:grid-cols-2">
                <div>
                  <dt className="text-xs text-slate-600">Entrypoint</dt>
                  <dd className="mt-1 font-mono text-sm text-slate-300">
                    {fn.entrypoint}
                  </dd>
                </div>
                <div>
                  <dt className="text-xs text-slate-600">Commands</dt>
                  <dd className="mt-1 font-mono text-sm text-slate-300">
                    {fn.commands || "—"}
                  </dd>
                </div>
                <div>
                  <dt className="text-xs text-slate-600">Timeout</dt>
                  <dd className="mt-1 text-sm text-slate-300">
                    {fn.timeout_seconds}s
                  </dd>
                </div>
                <div>
                  <dt className="text-xs text-slate-600">Logging</dt>
                  <dd className="mt-1 text-sm text-slate-300">
                    {fn.logging ? "Enabled" : "Disabled"}
                  </dd>
                </div>
              </dl>
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent value="deployments">
          <Card>
            <DataTable
              data={deployments.data?.deployments ?? []}
              columns={deploymentColumns}
              loading={deployments.isLoading}
              empty="No deployments yet."
              serverPagination={pageControls(
                deploymentsNavigation,
                nextCursor(deployments.data),
                deployments.isFetching,
              )}
            />
          </Card>
        </TabsContent>
        <TabsContent value="executions">
          <Card>
            <DataTable
              data={executions.data?.executions ?? []}
              columns={executionColumns}
              loading={executions.isLoading}
              empty="No executions yet."
              serverPagination={pageControls(
                executionsNavigation,
                nextCursor(executions.data),
                executions.isFetching,
              )}
            />
          </Card>
        </TabsContent>
        <TabsContent value="logs">
          <LogViewer
            key={activeDeploymentId ?? "no-deployment"}
            title="Active deployment build logs"
            description="Build output from the active deployment. Open an execution to inspect runtime logs."
            fetchPage={logs}
            enabled={Boolean(fn.active_deployment_id)}
            emptyMessage={
              fn.active_deployment_id
                ? "No build logs yet. Output will appear when the deployment starts."
                : "No active deployment. Build logs will appear after a deployment is activated."
            }
          />
        </TabsContent>
        <TabsContent value="configuration">
          <FunctionVariablesPanel
            projectId={projectId}
            functionId={functionId}
          />
        </TabsContent>
      </Tabs>
    </>
  );
}

export function FunctionDeploymentView({
  organizationId,
  projectId,
  functionId,
  deploymentId,
}: {
  organizationId: string;
  projectId: string;
  functionId: string;
  deploymentId: string;
}) {
  const query = useFunctionDeployment(projectId, functionId, deploymentId);
  const logFetcher = useCallback(
    async (after?: number): Promise<LogLine[]> => {
      const result = await api.GET(
        "/v1/projects/{projectID}/functions/{functionID}/deployments/{deploymentID}/logs",
        {
          params: {
            path: {
              projectID: projectId,
              functionID: functionId,
              deploymentID: deploymentId,
            },
            query: after === undefined ? {} : { after },
          },
        },
      );
      const data = await unwrap(result);
      return (data?.logs ?? []) as LogLine[];
    },
    [deploymentId, functionId, projectId],
  );
  const deployment = query.data?.deployment;
  if (query.error)
    return <ErrorState error={query.error} retry={() => query.refetch()} />;
  if (!deployment)
    return (
      <EmptyState
        title="Deployment not found"
        description="The deployment may have been removed or is outside this function."
      />
    );
  return (
    <>
      <BackLink
        href={`/organizations/${organizationId}/projects/${projectId}/functions/${functionId}`}
        label="Back to function"
      />
      <PageHeader
        eyebrow="Deployment"
        title={`Version ${deployment.version}`}
        description="Immutable deployment metadata and incremental build logs."
        actions={
          <StatusBadge status={getDeploymentLifecycleStatus(deployment)} />
        }
      />
      <div className="mb-5 flex flex-wrap items-center gap-2">
        <ResourceId id={deployment.id} label="Deployment ID" />
        <span className="text-xs text-slate-600">
          Updated {formatDate(deployment.updated_at)}
        </span>
        <span className="text-xs text-slate-600">
          Queued {formatDate(deployment.queued_at)}
        </span>
      </div>
      <div className="grid gap-4 md:grid-cols-4">
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-slate-500">Build</p>
            <div className="mt-2">
              <StatusBadge status={deployment.build_status} />
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-slate-500">Source</p>
            <p className="mt-2 text-sm text-white">{deployment.source}</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-slate-500">Size</p>
            <p className="mt-2 text-sm text-white">
              {formatBytes(deployment.size_bytes)}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-slate-500">Duration</p>
            <p className="mt-2 text-sm text-white">
              {formatDuration(deploymentDurationMs(deployment))}
            </p>
            {deployment.build_started_at ? (
              <p className="mt-1 text-xs text-slate-600">
                Started {formatDate(deployment.build_started_at)}
              </p>
            ) : null}
          </CardContent>
        </Card>
      </div>
      {isDeploymentInProgress(deployment) ? (
        <Card className="mt-5 border-violet-300/20 bg-violet-300/[0.04]">
          <CardContent className="p-5">
            <p className="text-sm font-medium text-violet-100">
              Build in progress
            </p>
            <p className="mt-1 text-xs leading-5 text-violet-200/70">
              This deployment is queued or building. Its status and build logs
              refresh automatically.
            </p>
          </CardContent>
        </Card>
      ) : null}
      {deployment.error_message ||
      getDeploymentLifecycleStatus(deployment) === "failed" ? (
        <Card className="mt-5 border-rose-300/20 bg-rose-400/[0.04]">
          <CardContent className="space-y-3 p-5">
            <div>
              <p className="text-xs uppercase tracking-[0.14em] text-rose-300">
                Deployment failed
              </p>
              <p className="mt-2 whitespace-pre-wrap text-sm text-rose-100">
                {deployment.error_message ??
                  "The build did not complete successfully."}
              </p>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button asChild variant="outline">
                <a href="#build-logs">View build logs</a>
              </Button>
              <Button asChild variant="ghost">
                <Link
                  href={`/organizations/${organizationId}/projects/${projectId}/functions/${functionId}`}
                >
                  Deploy another archive
                </Link>
              </Button>
            </div>
          </CardContent>
        </Card>
      ) : null}
      <div id="build-logs" className="mt-5 scroll-mt-6">
        <LogViewer
          key={deploymentId}
          title="Build logs"
          description="Backend sequence cursor; only new lines are requested while following."
          fetchPage={logFetcher}
          emptyMessage="No logs yet. Build output will appear when this deployment starts."
        />
      </div>
    </>
  );
}
