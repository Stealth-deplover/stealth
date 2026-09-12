"use client";

import { createContext, useContext, useMemo } from "react";
import type { ReactNode } from "react";
import { usePathname } from "next/navigation";
import { parseConsoleRoute, type ConsoleRoute } from "@/lib/console-routes";

const ConsoleRouteContext = createContext<ConsoleRoute | undefined>(undefined);

export function ConsoleRouteContextProvider({
  children,
}: Readonly<{ children: ReactNode }>) {
  const pathname = usePathname();
  const route = useMemo(() => parseConsoleRoute(pathname), [pathname]);

  return (
    <ConsoleRouteContext.Provider value={route}>
      {children}
    </ConsoleRouteContext.Provider>
  );
}

export function useConsoleRouteContext() {
  const route = useContext(ConsoleRouteContext);
  if (!route)
    throw new Error(
      "useConsoleRouteContext must be used within ConsoleRouteContextProvider",
    );
  return route;
}
