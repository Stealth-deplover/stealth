import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { DataTable, type DataTableColumnDef } from "./data-table";

type Row = { name: string };

const columns: DataTableColumnDef<Row>[] = [
  { accessorKey: "name", header: "Name" },
];

describe("DataTable", () => {
  it("announces table loading while keeping the table structure available", () => {
    render(<DataTable<Row> columns={columns} data={[]} loading />);

    expect(screen.getByRole("status")).toHaveTextContent("Loading table…");
    expect(screen.getByRole("table")).toBeInTheDocument();
  });

  it("uses the contextual empty message supplied by a domain view", () => {
    render(
      <DataTable<Row>
        columns={columns}
        data={[]}
        empty="No audit events recorded for this organization."
      />,
    );

    expect(
      screen.getByText("No audit events recorded for this organization."),
    ).toBeInTheDocument();
  });
});
