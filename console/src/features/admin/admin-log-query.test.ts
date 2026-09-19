import { describe, expect, it } from "vitest";
import { formatAdminLogQuery, parseAdminLogQuery } from "./admin-log-query";

describe("admin log query", () => {
  it("parses bounded field filters and quoted message text", () => {
    expect(
      parseAdminLogQuery(
        'service:"stealth api" level:ERROR message:"database timeout"',
      ),
    ).toEqual({
      filters: {
        service: "stealth api",
        level: "ERROR",
        trace_id: "",
        query: "database timeout",
      },
      diagnostics: [],
      valid: true,
    });
  });

  it("rejects unknown fields and unterminated quotes", () => {
    expect(parseAdminLogQuery("container:api").valid).toBe(false);
    expect(
      parseAdminLogQuery('message:"not closed').diagnostics[0],
    ).toMatchObject({
      message: expect.stringContaining("Close the quoted value"),
    });
  });

  it("round trips filters without exposing a SQL escape hatch", () => {
    const formatted = formatAdminLogQuery({
      service: "api edge",
      level: "ERROR",
      trace_id: "trace-123",
      query: "password=redacted",
    });
    expect(formatted).toBe(
      'service:"api edge" level:ERROR trace_id:trace-123 message:"password=redacted"',
    );
    const parsed = parseAdminLogQuery(formatted);
    expect(parsed.valid).toBe(true);
    expect(parsed.filters).toEqual({
      service: "api edge",
      level: "ERROR",
      trace_id: "trace-123",
      query: "password=redacted",
    });
  });
});
