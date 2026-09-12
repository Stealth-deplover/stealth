import type { components } from "@/api/generated/schema";
import type { CreateField } from "@/components/create-dialog";

export type UserFormValues = {
  email: string;
  password: string;
  name: string;
};

export const userFields: readonly CreateField<UserFormValues>[] = [
  { name: "email", label: "Email", type: "email" },
  {
    name: "password",
    label: "Temporary password",
    type: "password",
    help: "Minimum 12 characters.",
  },
  { name: "name", label: "Name", required: false },
];

export function userPayload(
  values: UserFormValues,
): components["schemas"]["CreateProjectUserRequest"] {
  const email = values.email.trim();
  if (!email || !values.password) {
    throw new Error("Email and password are required.");
  }
  return { email, password: values.password, name: values.name.trim() || null };
}
