"use client";
import { FunctionSquare } from "lucide-react";

export function ProjectResourceIntro({
  icon: Icon,
  title,
  description,
}: {
  icon: typeof FunctionSquare;
  title: string;
  description: string;
}) {
  return (
    <div className="mb-5 flex items-center gap-3 rounded-lg border border-graphite bg-carbon px-3.5 py-3">
      <div className="flex size-8 shrink-0 items-center justify-center rounded-md border border-graphite bg-white/[0.05] text-fog">
        <Icon className="size-4" aria-hidden="true" />
      </div>
      <p className="text-xs leading-5 text-fog">
        <span className="font-medium text-mist">{title}.</span> {description}
      </p>
    </div>
  );
}
