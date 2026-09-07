import Link from "next/link";
import { ArrowLeft } from "lucide-react";

export function BackLink({ href, label }: { href: string; label: string }) {
  return (
    <Link
      href={href}
      className="mb-5 inline-flex items-center gap-2 text-xs text-slate-500 hover:text-cyan-200"
    >
      <ArrowLeft className="size-3.5" /> {label}
    </Link>
  );
}
