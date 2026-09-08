import { humanize } from "@/lib/utils";

const deliveryStatusLabels: Record<string, string> = {
  pending: "Pending",
  running: "Delivering",
  succeeded: "Delivered",
  failed: "Failed",
};

export function webhookDeliveryStatusLabel(status: string | null | undefined) {
  if (!status) return "Unknown";
  return deliveryStatusLabels[status.toLowerCase()] ?? humanize(status);
}

export function formatAPIKeyScope(scope: string) {
  const [resource, permission] = scope.split(".", 2);
  if (!resource || !permission) return scope;
  return `${humanize(resource)} · ${humanize(permission)}`;
}

export function parseCommaSeparatedValues(value: string) {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}
