import { Button } from "@base-ui/react/button";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { NavLink } from "react-router-dom";
import { api, type Session } from "./api";
import { LogoMark } from "./auth";

export function Shell({ session, children }: { session: Session; children: ReactNode }) {
  const t = (key: string) => session.messages[key] ?? key;
  const queryClient = useQueryClient();
  const logout = useMutation({
    mutationFn: () => api("/api/logout", { method: "POST" }),
    onSuccess: () => {
      queryClient.clear();
      window.location.assign("/");
    },
  });
  const initial = session.email.trim().charAt(0).toUpperCase() || "?";

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand-row">
          <NavLink className="logo" to="/admin">
            <LogoMark />
            <span>{t("brand")}</span>
          </NavLink>
        </div>
        <nav>
          <NavLink className="nav-item" to="/admin" end>
            <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
              <rect x="2" y="2" width="5" height="5" rx="1.2" fill="currentColor" />
              <rect x="9" y="2" width="5" height="5" rx="1.2" fill="currentColor" opacity=".75" />
              <rect x="2" y="9" width="5" height="5" rx="1.2" fill="currentColor" opacity=".75" />
              <rect x="9" y="9" width="5" height="5" rx="1.2" fill="currentColor" opacity=".5" />
            </svg>
            {t("sites")}
          </NavLink>
        </nav>
        <div className="account" aria-label={t("signed_in_as")}>
          <span className="avatar" aria-hidden="true">
            {initial}
          </span>
          <span className="account-email" title={session.email}>
            {session.email}
          </span>
          <Button type="button" className="sign-out" disabled={logout.isPending} onClick={() => logout.mutate()}>
            {t("sign_out")}
          </Button>
        </div>
      </aside>
      <div className="workspace">
        <main className="canvas">
          <div className="page-tools">
            <LangSwitch lang={session.lang} label={t("language")} />
          </div>
          {children}
        </main>
      </div>
    </div>
  );
}

export function LangSwitch({ lang, label }: { lang: string; label: string }) {
  return (
    <nav className="lang-switch" aria-label={label}>
      <a href="/lang?set=zh" aria-current={lang === "zh" ? "true" : undefined}>
        中
      </a>
      <span aria-hidden="true">/</span>
      <a href="/lang?set=en" aria-current={lang === "en" ? "true" : undefined}>
        EN
      </a>
    </nav>
  );
}
