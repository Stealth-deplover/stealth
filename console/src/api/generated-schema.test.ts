import { describe, expect, it } from "vitest";
import {
  DatabaseRowsTransactionOperationAction,
  type components,
} from "@/api/generated/schema";

describe("generated dynamic database row data", () => {
  it("accepts arbitrary imported and transaction row values", () => {
    const imported: components["schemas"]["ImportDatabaseRow"] = {
      data: {
        email: "ada@example.com",
        active: true,
        profile: { plan: "pro" },
      },
    };
    const operation: components["schemas"]["DatabaseRowsTransactionOperation"] =
      {
        action: DatabaseRowsTransactionOperationAction.update,
        data: { attempts: 3, deleted_at: null },
      };

    expect(imported.data.email).toBe("ada@example.com");
    expect(operation.data?.profile ?? operation.data?.attempts).toBe(3);
  });
});
