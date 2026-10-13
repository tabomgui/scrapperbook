"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

const TABS = [
  { href: "/", label: "Anúncios" },
  { href: "/searches", label: "Buscas" },
  { href: "/health", label: "Saúde" },
];

export function NavTabs() {
  const pathname = usePathname();
  return (
    <nav className="flex gap-4 text-sm">
      {TABS.map((tab) => {
        const active = tab.href === "/" ? pathname === "/" : pathname.startsWith(tab.href);
        return (
          <Link
            key={tab.href}
            href={tab.href}
            className={
              active
                ? "border-b-2 border-slate-900 pb-2 font-medium text-slate-900"
                : "border-b-2 border-transparent pb-2 text-slate-500 hover:text-slate-900"
            }
          >
            {tab.label}
          </Link>
        );
      })}
    </nav>
  );
}
