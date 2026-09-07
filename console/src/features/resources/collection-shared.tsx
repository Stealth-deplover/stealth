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
    <div className="mb-5 flex items-center gap-3 rounded-lg border border-stealth-border bg-white/[0.015] px-3.5 py-3">
      <div className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-cyan-300/20 bg-cyan-300/10 text-cyan-200">
        <Icon className="size-4" />
      </div>
      <p className="text-xs leading-5 text-slate-500">
        <span className="font-medium text-slate-300">{title}.</span>{" "}
        {description}
      </p>
    </div>
  );
}
