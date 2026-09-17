import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { DatabaseColumnType } from "@/api/generated/schema";
import type { DatabaseColumn } from "@/api/types";
import { displayRowValue, parseColumn, parseRowData } from "./data-values";
import { RowValue } from "./row-value";

const columns: DatabaseColumn[] = [
  {
    id: "column-1",
    table_id: "table-1",
    key: "email",
    type: DatabaseColumnType.varchar,
    required: true,
    varchar_size: 50,
    created_at: "",
    updated_at: "",
  },
  {
    id: "column-2",
    table_id: "table-1",
    key: "enabled",
    type: DatabaseColumnType.boolean,
    required: false,
    created_at: "",
    updated_at: "",
  },
];

describe("typed row data", () => {
  it("keeps false, null, and typed values without coercion", () => {
    expect(
      parseRowData('{"email":"ada@example.com","enabled":false}', columns),
    ).toEqual({ email: "ada@example.com", enabled: false });
    expect(parseRowData('{"enabled":null}', columns, true)).toEqual({
      enabled: null,
    });
  });
  it("rejects missing required fields, unknown keys, and incorrect types", () => {
    expect(() => parseRowData("{}", columns)).toThrow("email is required");
    expect(() => parseRowData('{"id":"test"}', columns)).toThrow(
      "Unknown column",
    );
    expect(() => parseRowData('{"enabled":"false"}', columns, true)).toThrow(
      "expected boolean",
    );
    expect(() => parseRowData("[]", columns)).toThrow("JSON object");
    expect(() => parseRowData("invalid", columns)).toThrow("valid JSON");
  });
  it("validates varchar size and defaults when creating schema", () => {
    expect(() =>
      parseColumn({
        key: "email",
        type: "varchar",
        required: "true",
        varchar_size: "",
        default: "",
      }),
    ).toThrow();
    expect(() =>
      parseColumn({
        key: "enabled",
        type: "boolean",
        required: "true",
        varchar_size: "",
        default: '"false"',
      }),
    ).toThrow();
    expect(
      parseColumn({
        key: "enabled",
        type: "boolean",
        required: "false",
        varchar_size: "",
        default: "false",
      }).default,
    ).toBe(false);
  });
  it("renders JSON and markup as text, with separate null and boolean values", () => {
    const { container } = render(
      <>
        <RowValue value={null} />
        <RowValue value={false} />
        <RowValue value={'<img src=x onerror="alert(1)">'} expanded />
        <RowValue value={{ nested: [1, true] }} expanded />
      </>,
    );
    expect(screen.getByText("null")).toBeVisible();
    expect(screen.getByText("false")).toBeVisible();
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toContain('"nested"');
    expect(displayRowValue(0)).toBe("0");
    expect(displayRowValue(undefined)).toBe("Not available");
  });
});
