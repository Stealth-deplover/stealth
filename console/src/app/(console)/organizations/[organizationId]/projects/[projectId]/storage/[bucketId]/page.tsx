import { BucketDetailView } from "@/features/resources/detail-views";

export default async function BucketPage({ params }: { params: Promise<{ organizationId: string; projectId: string; bucketId: string }> }) { const { organizationId, projectId, bucketId } = await params; return <BucketDetailView organizationId={organizationId} projectId={projectId} bucketId={bucketId} />; }
