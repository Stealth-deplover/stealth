import type { components } from "@/api/generated/schema";
import type { CreateField } from "@/components/create-dialog";
import {
  bucketName,
  objectName,
  positiveBytes,
} from "@/features/storage/storage-values";

export type StorageObjectFormValues = {
  name: string;
};

export function storageObjectFields(
  defaultName: string,
): readonly CreateField<StorageObjectFormValues>[] {
  return [{ name: "name", label: "Object name", defaultValue: defaultName }];
}

export function storageObjectPayload(
  values: StorageObjectFormValues,
): components["schemas"]["UpdateStorageFileRequest"] {
  return { name: objectName.parse(values.name) };
}

export type StorageBucketSettingsFormValues = {
  name: string;
  max_file_size_bytes: string;
  quota_bytes: string;
};

export function storageBucketSettingsFields(
  defaults: StorageBucketSettingsFormValues,
): readonly CreateField<StorageBucketSettingsFormValues>[] {
  return [
    {
      name: "name",
      label: "Bucket name",
      defaultValue: defaults.name,
    },
    {
      name: "max_file_size_bytes",
      label: "Maximum object size (bytes)",
      defaultValue: defaults.max_file_size_bytes,
    },
    {
      name: "quota_bytes",
      label: "Quota (bytes)",
      defaultValue: defaults.quota_bytes,
    },
  ];
}

export function storageBucketSettingsPayload(
  values: StorageBucketSettingsFormValues,
  usedBytes: number,
): components["schemas"]["UpdateStorageBucketRequest"] {
  const quota = positiveBytes(values.quota_bytes);
  if (quota < usedBytes)
    throw new Error("Quota cannot be smaller than current usage.");
  return {
    name: bucketName.parse(values.name),
    max_file_size_bytes: positiveBytes(values.max_file_size_bytes),
    quota_bytes: quota,
  };
}
