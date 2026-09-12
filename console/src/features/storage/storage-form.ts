import type { components } from "@/api/generated/schema";
import type { CreateField } from "@/components/create-dialog";
import { bucketName } from "@/features/storage/storage-values";

export type StorageFormValues = {
  name: string;
};

export const storageFields: readonly CreateField<StorageFormValues>[] = [
  { name: "name", label: "Name", placeholder: "assets" },
];

export function storagePayload(
  values: StorageFormValues,
): components["schemas"]["CreateStorageBucketRequest"] {
  return { name: bucketName.parse(values.name), file_security: true };
}
