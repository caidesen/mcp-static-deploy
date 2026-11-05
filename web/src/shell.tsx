import { Button } from "@base-ui/react/button";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Link } from "react-router-dom";
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

  return (
    <div className="shell">
      <header className="topbar">
        <div className="topbar-inner">
          <Link className="logo" to="/admin">
            <LogoMark />
            <span>{t("brand")}</span>
          </Link>
          <div className="topbar-actions">
            <LangSwitch lang={session.lang} label={t("language")} />
            <span className="account-email" title={session.email}>
              {session.email}
            </span>
            <Button type="button" className="sign-out" disabled={logout.isPending} onClick={() => logout.mutate()}>
              {t("sign_out")}
            </Button>
          </div>
        </div>
      </header>
      <main className="column">{children}</main>
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
