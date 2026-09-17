import Link from "next/link";
import { ArrowLeft } from "lucide-react";

export function BackLink({ href, label }: { href: string; label: string }) {
  return (
    <Link
      href={href}
      className="mb-5 inline-flex min-h-11 items-center gap-2 text-xs text-fog hover:text-acid-lime"
    >
      <ArrowLeft className="size-3.5" aria-hidden="true" /> {label}
    </Link>
  );
}
