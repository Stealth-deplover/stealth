import { z } from "zod";

export const bucketName = z
  .string()
  .trim()
  .regex(
    /^[a-z0-9][a-z0-9-]{1,62}$/,
    "Use 2–63 lowercase letters, numbers, or hyphens, starting with a letter or number.",
  );
export const objectName = z
  .string()
  .min(1)
  .max(255)
  .refine(
    (name) =>
      name !== "." && name !== ".." && !/[\\/\u0000-\u001f\u007f]/.test(name),
    "Use a filename without path separators or control characters.",
  );

export function positiveBytes(value: string) {
  const bytes = Number(value);
  if (!Number.isSafeInteger(bytes) || bytes <= 0)
    throw new Error("Size must be a positive whole number of bytes.");
  return bytes;
}
