import { describe, expect, it } from "vitest";
import { DatabaseColumnType } from "@/api/generated/schema";
import type { DatabaseColumn } from "@/api/types";
import { databaseTablePayload } from "@/features/databases/database-table-form";
import {
  databaseColumnPayload,
  databasePartialRowPayload,
  databaseRowPayload,
} from "@/features/databases/table-form";
import {
  storageBucketSettingsPayload,
  storageObjectPayload,
} from "@/features/storage/bucket-form";

const columns: DatabaseColumn[] = [
  {
    id: "column-id",
    table_id: "table-id",
    key: "email",
    type: DatabaseColumnType.text,
    required: true,
    varchar_size: null,
    default: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
  {
    id: "column-id-2",
    table_id: "table-id",
    key: "enabled",
    type: DatabaseColumnType.boolean,
    required: false,
    varchar_size: null,
    default: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
];

describe("data form adapters", () => {
  it("keeps database table defaults and naming validation in the adapter", () => {
    expect(
      databaseTablePayload({ name: " users ", row_security: "true" }),
    ).toEqual({ name: "users", row_security: true });
    expect(() =>
      databaseTablePayload({ name: "x", row_security: "true" }),
    ).toThrow();
  });

  it("maps typed row create and partial update payloads", () => {
    expect(
      databaseRowPayload(
        { data: '{"email":"ada@example.test","enabled":true}' },
        columns,
      ),
    ).toEqual({
      data: { email: "ada@example.test", enabled: true },
    });
    expect(
      databasePartialRowPayload({ data: '{"enabled":null}' }, columns),
    ).toEqual({ data: { enabled: null } });
  });

  it("keeps column parsing and storage constraints out of views", () => {
    expect(
      databaseColumnPayload({
        key: " age ",
        type: "integer",
        required: "true",
        varchar_size: "",
        default: "42",
      }),
    ).toEqual({
      key: "age",
      type: "integer",
      required: true,
      default: 42,
    });
    expect(storageObjectPayload({ name: "avatar.png" })).toEqual({
      name: "avatar.png",
    });
    expect(
      storageBucketSettingsPayload(
        {
          name: " assets ",
          max_file_size_bytes: "1024",
          quota_bytes: "4096",
        },
        512,
      ),
    ).toEqual({
      name: "assets",
      max_file_size_bytes: 1024,
      quota_bytes: 4096,
    });
    expect(() =>
      storageBucketSettingsPayload(
        {
          name: "assets",
          max_file_size_bytes: "1024",
          quota_bytes: "256",
        },
        512,
      ),
    ).toThrow();
  });
});
