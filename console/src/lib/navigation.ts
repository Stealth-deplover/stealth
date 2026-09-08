const fallbackPath = "/organizations";
const consoleOrigin = "https://stealth-console.invalid";

export function getSafeNextPath(value: string | null | undefined) {
  if (!value || !value.startsWith("/") || value.startsWith("//"))
    return fallbackPath;
  try {
    const resolved = new URL(value, consoleOrigin);
    return resolved.origin === consoleOrigin ? value : fallbackPath;
  } catch {
    return fallbackPath;
  }
}
