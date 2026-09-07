"use client";

import { ErrorState } from "@/components/feedback/error-state";

export default function GlobalError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return (
    <main className="mx-auto flex min-h-screen max-w-2xl items-center px-6">
      <ErrorState
        error={error}
        retry={reset}
        title="Something interrupted this view"
      />
    </main>
  );
}
