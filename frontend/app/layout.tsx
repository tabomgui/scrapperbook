import type { Metadata } from "next";
import "./globals.css";
import { NavTabs } from "@/components/NavTabs";

export const metadata: Metadata = {
  title: "Marketplace Monitor",
  description: "Dashboard do marketplace monitor",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="pt-BR">
      <body>
        <header className="border-b border-slate-200 bg-white">
          <div className="mx-auto max-w-6xl px-4 py-3">
            <div className="mb-2 text-lg font-semibold">Marketplace Monitor</div>
            <NavTabs />
          </div>
        </header>
        <main className="mx-auto max-w-6xl px-4 py-6">{children}</main>
      </body>
    </html>
  );
}
