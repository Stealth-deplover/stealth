import { FunctionExecutionView } from "@/features/functions/function-execution-view";

export default async function FunctionExecutionPage({
  params,
}: {
  params: Promise<{
    organizationId: string;
    projectId: string;
    functionId: string;
    executionId: string;
  }>;
}) {
  const { organizationId, projectId, functionId, executionId } = await params;
  return (
    <FunctionExecutionView
      organizationId={organizationId}
      projectId={projectId}
      functionId={functionId}
      executionId={executionId}
    />
  );
}
