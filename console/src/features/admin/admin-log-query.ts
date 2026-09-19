export type AdminLogFilters = {
  service: string;
  level: string;
  trace_id: string;
  query: string;
};

export type AdminLogQueryDiagnostic = {
  from: number;
  to: number;
  message: string;
};

export type AdminLogQueryParseResult = {
  filters: AdminLogFilters;
  diagnostics: AdminLogQueryDiagnostic[];
  valid: boolean;
};

const knownFields = new Set(["service", "level", "trace_id", "message"]);

type Token = {
  value: string;
  from: number;
  to: number;
};

function readToken(
  input: string,
  start: number,
): {
  token?: Token;
  next: number;
  diagnostic?: AdminLogQueryDiagnostic;
} {
  let index = start;
  let value = "";
  while (index < input.length && !/\s/.test(input[index])) {
    if (input[index] !== '"') {
      value += input[index];
      index += 1;
      continue;
    }

    index += 1;
    let closed = false;
    while (index < input.length) {
      const character = input[index];
      if (character === "\\") {
        const escaped = input[index + 1];
        if (escaped === undefined) {
          return {
            next: input.length,
            diagnostic: {
              from: start,
              to: input.length,
              message: "Escape the final character or close the quoted value.",
            },
          };
        }
        value += escaped;
        index += 2;
        continue;
      }
      if (character === '"') {
        closed = true;
        index += 1;
        break;
      }
      value += character;
      index += 1;
    }
    if (!closed) {
      return {
        next: input.length,
        diagnostic: {
          from: start,
          to: input.length,
          message: "Close the quoted value before the end of the query.",
        },
      };
    }
  }

  return { token: { value, from: start, to: index }, next: index };
}

export function parseAdminLogQuery(input: string): AdminLogQueryParseResult {
  const filters: AdminLogFilters = {
    service: "",
    level: "",
    trace_id: "",
    query: "",
  };
  const diagnostics: AdminLogQueryDiagnostic[] = [];
  const freeText: string[] = [];
  let index = 0;

  while (index < input.length) {
    while (index < input.length && /\s/.test(input[index])) index += 1;
    if (index >= input.length) break;

    const result = readToken(input, index);
    if (result.diagnostic) {
      diagnostics.push(result.diagnostic);
      break;
    }
    if (!result.token) break;
    index = result.next;
    const separator = result.token.value.indexOf(":");
    if (separator <= 0) {
      if (result.token.value) freeText.push(result.token.value);
      continue;
    }

    const field = result.token.value.slice(0, separator).toLowerCase();
    const value = result.token.value.slice(separator + 1).trim();
    if (!knownFields.has(field)) {
      diagnostics.push({
        from: result.token.from,
        to: result.token.to,
        message: `Unknown filter field “${field}”. Use service, level, trace_id, or message.`,
      });
      continue;
    }
    if (!value) {
      diagnostics.push({
        from: result.token.from,
        to: result.token.to,
        message: `${field} needs a value.`,
      });
      continue;
    }
    switch (field) {
      case "message":
        freeText.push(value);
        break;
      case "service":
        filters.service = value;
        break;
      case "level":
        filters.level = value;
        break;
      case "trace_id":
        filters.trace_id = value;
        break;
    }
  }

  filters.query = freeText.join(" ");
  return { filters, diagnostics, valid: diagnostics.length === 0 };
}

export function formatAdminLogQuery(filters: Partial<AdminLogFilters>): string {
  const parts: string[] = [];
  for (const field of ["service", "level", "trace_id"] as const) {
    const value = filters[field]?.trim();
    if (value) parts.push(`${field}:${quoteQueryValue(value)}`);
  }
  const query = filters.query?.trim();
  if (query) parts.push(`message:${quoteQueryValue(query)}`);
  return parts.join(" ");
}

function quoteQueryValue(value: string): string {
  if (/^[A-Za-z0-9_./:@+-]+$/.test(value)) return value;
  return `"${value.replaceAll("\\", "\\\\").replaceAll('"', '\\"')}"`;
}
