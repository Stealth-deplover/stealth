import { Badge } from "@/components/ui/badge";
import { displayRowValue } from "./data-values";

export function RowValue({
  value,
  type,
  expanded = false,
}: {
  value: unknown;
  type?: string;
  expanded?: boolean;
}) {
  const display = displayRowValue(value, type);
  if (value === null || value === undefined)
    return <span className="text-slate-500">{display}</span>;
  if (typeof value === "boolean")
    return <Badge variant="neutral">{display}</Badge>;
  if (expanded)
    return (
      <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words font-mono text-xs">
        {display}
      </pre>
    );
  return (
    <span className="block max-w-64 truncate text-xs" title={display}>
      {display}
    </span>
  );
}
