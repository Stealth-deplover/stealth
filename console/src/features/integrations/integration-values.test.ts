import { describe, expect, it } from "vitest";
import {
  formatAPIKeyScope,
  parseCommaSeparatedValues,
  webhookDeliveryStatusLabel,
} from "@/features/integrations/integration-values";

describe("integration display values", () => {
  it.each([
    ["pending", "Pending"],
    ["running", "Delivering"],
    ["succeeded", "Delivered"],
    ["failed", "Failed"],
  ])("maps delivery status %s to %s", (status, label) => {
    expect(webhookDeliveryStatusLabel(status)).toBe(label);
  });

  it("keeps unknown delivery statuses readable", () => {
    expect(webhookDeliveryStatusLabel("paused_by_policy")).toBe(
      "Paused By Policy",
    );
  });

  it("formats scopes without changing their backend value", () => {
    expect(formatAPIKeyScope("webhooks.write")).toBe("Webhooks · Write");
  });

  it("parses comma-separated form values", () => {
    expect(parseCommaSeparatedValues(" users.read, , storage.write ")).toEqual([
      "users.read",
      "storage.write",
    ]);
  });
});
