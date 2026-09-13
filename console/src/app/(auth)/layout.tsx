export default function AuthLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <main className="grid min-h-screen bg-stealth-bg lg:grid-cols-[1.05fr_.95fr]">
      <section className="relative hidden overflow-hidden border-r border-stealth-border p-12 lg:flex lg:flex-col lg:justify-between">
        <div className="relative">
          <div className="flex items-center gap-2.5">
            <span className="flex size-9 items-center justify-center rounded-xl border border-cyan-300/30 bg-cyan-300/10 font-black text-cyan-200">
              S
            </span>
            <span className="text-sm font-semibold text-white">Stealth</span>
          </div>
          <div className="mt-28 max-w-xl">
            <p className="mb-4 text-xs font-medium uppercase tracking-[0.22em] text-cyan-300">
              Developer operating console
            </p>
            <h1 className="text-5xl font-semibold leading-[1.05] tracking-[-0.04em] text-white">
              Make the platform legible.
            </h1>
            <p className="mt-6 max-w-md text-base leading-7 text-slate-400">
              A focused control plane for the services, deploys, data, and
              signals that make your product run.
            </p>
          </div>
        </div>
        <p className="relative text-xs text-slate-600">
          Stealth Console · API-first by design
        </p>
      </section>
      <section className="flex items-center justify-center px-6 py-12">
        {children}
      </section>
    </main>
  );
}
