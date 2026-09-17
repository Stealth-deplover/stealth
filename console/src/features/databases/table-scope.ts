export type TableScope = {
  projectId: string;
  databaseId: string;
  tableId: string;
};

export type TableURLUpdate = (
  updates: Record<string, string | undefined>,
  resetCursor?: boolean,
) => void;
