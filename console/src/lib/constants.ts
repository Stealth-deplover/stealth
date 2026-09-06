export const APP_NAME = process.env.NEXT_PUBLIC_APP_NAME ?? "Stealth Console";

export const resourceAccent = {
  function: "text-cyan-300 bg-cyan-400/10 border-cyan-300/20",
  site: "text-violet-300 bg-violet-400/10 border-violet-300/20",
  database: "text-amber-300 bg-amber-400/10 border-amber-300/20",
  storage: "text-emerald-300 bg-emerald-400/10 border-emerald-300/20",
  webhook: "text-pink-300 bg-pink-400/10 border-pink-300/20",
  agent: "text-orange-300 bg-orange-400/10 border-orange-300/20",
} as const;
