export default function AuthLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <main className="grid min-h-screen bg-void lg:grid-cols-[1.05fr_.95fr]">
      <a
        href="#auth-main-content"
        className="sr-only fixed left-4 top-4 z-[60] rounded-md bg-acid-lime px-3 py-2 text-sm font-medium text-void focus:not-sr-only"
      >
        Skip to content
      </a>
      <section className="relative hidden overflow-hidden border-r border-graphite bg-carbon/35 p-12 lg:flex lg:flex-col lg:justify-between">
        <div className="relative">
          <div className="flex items-center gap-2.5">
            <span className="flex size-9 items-center justify-center rounded-md border border-acid-lime/40 bg-acid-lime text-sm font-semibold text-void">
              S
            </span>
            <span className="text-sm font-semibold tracking-[-0.012em] text-paper">
              Stealth
            </span>
          </div>
          <div className="mt-28 max-w-xl">
            <p className="mb-4 text-xs font-medium uppercase tracking-[0.14em] text-fog">
              Developer operating console
            </p>
            <h1 className="text-5xl font-semibold leading-[1.02] tracking-[-0.022em] text-paper">
              Make the platform legible.
            </h1>
            <p className="mt-6 max-w-md text-base leading-7 text-fog">
              A focused control plane for the services, deploys, data, and
              signals that make your product run.
            </p>
          </div>
        </div>
        <p className="relative text-xs text-fog">
          Stealth Console · API-first by design
        </p>
      </section>
      <section
        id="auth-main-content"
        className="flex items-center justify-center px-4 py-10 sm:px-6 sm:py-12"
      >
        {children}
      </section>
    </main>
  );
}
