export type ConsoleRoute = {
  pathname: string;
  organizationId?: string;
  projectId?: string;
  organizationBase?: string;
  projectBase?: string;
  organizationSegments: readonly string[];
  projectSegments: readonly string[];
};

function path(...segments: readonly string[]) {
  return `/${segments.map(encodeURIComponent).join("/")}`;
}

export function organizationsPath() {
  return "/organizations";
}

export function organizationPath(
  organizationId: string,
  ...segments: readonly string[]
) {
  return path("organizations", organizationId, ...segments);
}

export function organizationProjectsPath(organizationId: string) {
  return organizationPath(organizationId, "projects");
}

export function projectPath(
  organizationId: string,
  projectId: string,
  ...segments: readonly string[]
) {
  return organizationPath(organizationId, "projects", projectId, ...segments);
}

export function parseConsoleRoute(pathname: string): ConsoleRoute {
  const segments = pathname.split("/").filter(Boolean);
  if (segments[0] !== "organizations" || !segments[1]) {
    return { pathname, organizationSegments: [], projectSegments: [] };
  }

  const organizationId = segments[1];
  const organizationSegments = segments.slice(2);
  const projectId =
    segments[2] === "projects" && segments[3] ? segments[3] : undefined;
  const projectSegments = projectId ? segments.slice(4) : [];
  const organizationBase = organizationPath(organizationId);

  return {
    pathname,
    organizationId,
    projectId,
    organizationBase,
    projectBase: projectId ? projectPath(organizationId, projectId) : undefined,
    organizationSegments,
    projectSegments,
  };
}
