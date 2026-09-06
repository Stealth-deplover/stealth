import type { ReactNode } from "react";
import { Blocks, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";

export function EmptyState({ title, description, action, actionLabel }: { title: string; description: string; action?: () => void; actionLabel?: string; icon?: ReactNode }) {
  return <Card className="border-dashed bg-transparent">
    <CardContent className="flex flex-col items-center justify-center px-6 py-16 text-center">
      <div className="mb-4 flex size-12 items-center justify-center rounded-2xl border border-cyan-300/20 bg-cyan-300/10 text-cyan-200"><Blocks className="size-5" /></div>
      <h3 className="text-sm font-semibold text-white">{title}</h3>
      <p className="mt-2 max-w-md text-sm leading-6 text-stealth-muted">{description}</p>
      {action && actionLabel ? <Button className="mt-5" onClick={action}><Plus className="size-4" /> {actionLabel}</Button> : null}
    </CardContent>
  </Card>;
}
