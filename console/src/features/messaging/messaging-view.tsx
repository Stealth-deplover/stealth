"use client";
import { nextCursor } from "@/api/pagination";
import {
  useMessagingMessages,
  useMessagingProviders,
  useMessagingTopics,
} from "@/api/queries";
import { CursorPaginationControls } from "@/components/cursor-pagination-controls";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { StatusBadge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";

export function MessagingView({ projectId }: { projectId: string }) {
  const providersNavigation = useCursorPagination("providers_cursor");
  const topicsNavigation = useCursorPagination("topics_cursor");
  const messagesNavigation = useCursorPagination("messages_cursor");
  const providers = useMessagingProviders(projectId, {
    cursor: providersNavigation.cursor,
  });
  const topics = useMessagingTopics(projectId, {
    cursor: topicsNavigation.cursor,
  });
  const messages = useMessagingMessages(projectId, {
    cursor: messagesNavigation.cursor,
  });
  const error = providers.error ?? topics.error ?? messages.error;
  if (error)
    return (
      <ErrorState
        error={error}
        retry={() => {
          void providers.refetch();
          void topics.refetch();
          void messages.refetch();
        }}
      />
    );
  return (
    <>
      <PageHeader
        eyebrow="Integrations"
        title="Messaging"
        description="Provider credentials and message content stay protected by the backend. The console exposes safe metadata only."
      />
      <div className="grid gap-4 md:grid-cols-3">
        <Card>
          <CardContent className="p-5">
            <p className="text-xs text-slate-500">Providers on this page</p>
            <p className="mt-2 text-2xl font-semibold text-white">
              {providers.data?.providers.length ?? "—"}
            </p>
            <p className="mt-2 text-xs text-slate-600">
              Encrypted credentials are never returned.
            </p>
            <CursorPaginationControls
              {...{
                canFirst: providersNavigation.canFirst,
                canPrevious: providersNavigation.canPrevious,
                canNext: Boolean(nextCursor(providers.data)),
                onFirst: providersNavigation.goFirst,
                onPrevious: providersNavigation.goPrevious,
                onNext: () =>
                  providersNavigation.goNext(nextCursor(providers.data)),
                isFetching: providers.isFetching,
              }}
              label="Provider page"
            />
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-5">
            <p className="text-xs text-slate-500">Topics on this page</p>
            <p className="mt-2 text-2xl font-semibold text-white">
              {topics.data?.topics.length ?? "—"}
            </p>
            <CursorPaginationControls
              {...{
                canFirst: topicsNavigation.canFirst,
                canPrevious: topicsNavigation.canPrevious,
                canNext: Boolean(nextCursor(topics.data)),
                onFirst: topicsNavigation.goFirst,
                onPrevious: topicsNavigation.goPrevious,
                onNext: () => topicsNavigation.goNext(nextCursor(topics.data)),
                isFetching: topics.isFetching,
              }}
              label="Topic page"
            />
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-5">
            <p className="text-xs text-slate-500">Messages on this page</p>
            <p className="mt-2 text-2xl font-semibold text-white">
              {messages.data?.messages.length ?? "—"}
            </p>
            <p className="mt-2 text-xs text-slate-600">
              Metadata only; content remains encrypted.
            </p>
            <CursorPaginationControls
              {...{
                canFirst: messagesNavigation.canFirst,
                canPrevious: messagesNavigation.canPrevious,
                canNext: Boolean(nextCursor(messages.data)),
                onFirst: messagesNavigation.goFirst,
                onPrevious: messagesNavigation.goPrevious,
                onNext: () =>
                  messagesNavigation.goNext(nextCursor(messages.data)),
                isFetching: messages.isFetching,
              }}
              label="Message page"
            />
          </CardContent>
        </Card>
      </div>
      <Card className="mt-5">
        <CardHeader>
          <CardTitle>Topics</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2">
          {topics.data?.topics.length ? (
            topics.data.topics.map((topic) => (
              <div
                key={topic.id}
                className="flex items-center justify-between rounded-lg border border-stealth-border px-3 py-3"
              >
                <div>
                  <p className="font-medium text-white">{topic.name}</p>
                  <p className="text-xs text-slate-600">
                    {topic.subscriber_count} subscribers
                  </p>
                </div>
                <StatusBadge status={topic.enabled ? "active" : "inactive"} />
              </div>
            ))
          ) : (
            <p className="text-sm text-slate-500">No topics returned.</p>
          )}
        </CardContent>
      </Card>
    </>
  );
}
