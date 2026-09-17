import type { components } from "@/api/generated/schema";
import type { CreateField } from "@/components/create-dialog";
import { parseCommaSeparatedValues } from "@/features/integrations/integration-values";

export type WebhookFormValues = {
  name: string;
  url: string;
  events: string;
  enabled: string;
};

export function webhookFields(
  defaults: Partial<WebhookFormValues> = {},
): readonly CreateField<WebhookFormValues>[] {
  const values = {
    name: "",
    url: "",
    events: "*",
    enabled: "true",
    ...defaults,
  };
  return [
    {
      name: "name",
      label: "Name",
      placeholder: "production-events",
      defaultValue: values.name,
    },
    {
      name: "url",
      label: "HTTPS URL",
      type: "url",
      placeholder: "https://example.com/hooks",
      defaultValue: values.url,
    },
    {
      name: "events",
      label: "Events",
      required: false,
      defaultValue: values.events,
      help: "Comma-separated event names, or * for all. The Go API validates event names.",
    },
    {
      name: "enabled",
      label: "State",
      type: "select",
      defaultValue: values.enabled,
      options: [
        { value: "true", label: "Enabled" },
        { value: "false", label: "Disabled" },
      ],
    },
  ];
}

function normalizedWebhookValues(values: WebhookFormValues) {
  const name = values.name.trim();
  const url = values.url.trim();
  if (!name || !url) throw new Error("Name and HTTPS URL are required.");
  return {
    name,
    url,
    events: parseCommaSeparatedValues(values.events),
    enabled: values.enabled !== "false",
  };
}

export function webhookPayload(
  values: WebhookFormValues,
): components["schemas"]["CreateWebhookRequest"] {
  return normalizedWebhookValues(values);
}

export function webhookUpdatePayload(
  values: WebhookFormValues,
): components["schemas"]["UpdateWebhookRequest"] {
  return normalizedWebhookValues(values);
}
