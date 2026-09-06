import { ConsoleShell } from "@/components/layout/console-shell";

export default function ConsoleLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return <ConsoleShell>{children}</ConsoleShell>;
}
