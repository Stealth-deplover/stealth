export const APP_NAME = process.env.NEXT_PUBLIC_APP_NAME ?? "Stealth Console";

export const resourceAccent = {
  function: "text-signal-teal bg-signal-teal/10 border-signal-teal/20",
  site: "text-iris-violet bg-iris-violet/10 border-iris-violet/20",
  app: "text-amber-200 bg-amber-300/10 border-amber-300/20",
  database: "text-lavender bg-lavender/10 border-lavender/20",
  storage: "text-pulse-green bg-pulse-green/10 border-pulse-green/20",
  webhook: "text-fog bg-white/[0.04] border-graphite",
  agent: "text-mist bg-white/[0.06] border-graphite",
} as const;
