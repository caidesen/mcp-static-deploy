import { Button } from "@base-ui/react/button";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, api, type Session, type Site } from "./api";

export function SitesPage({ session }: { session: Session }) {
  const t = (key: string) => session.messages[key] ?? key;
  const queryClient = useQueryClient();
  const [error, setError] = useState("");
  const sites = useQuery({
    queryKey: ["sites"],
    queryFn: () => api<Site[]>("/api/sites"),
  });
  const create = useMutation({
    mutationFn: () => api<Site>("/api/sites", { method: "POST" }),
    onSuccess: () => {
      setError("");
      queryClient.invalidateQueries({ queryKey: ["sites"] });
    },
    onError: (err: Error) => setError(err instanceof ApiError ? t(err.message) : t("err_internal")),
  });

  return (
    <div className="studio">
      <div className="page-head">
        <div>
          <h1>{t("sites")}</h1>
          <p className="subtitle">{t("site_hint")}</p>
        </div>
        <Button type="button" className="primary" disabled={create.isPending} onClick={() => create.mutate()}>
          + {t("create_site")}
        </Button>
      </div>
      {error ? (
        <p className="error" role="alert">
          {error}
        </p>
      ) : null}
      <div className="app-grid">
        {sites.data && sites.data.length > 0 ? (
          sites.data.map((site) => (
            <article className="app-card" key={site.slug}>
              <Link className="app-main" to={`/admin/sites/${site.slug}`}>
                <span className={`app-icon icon-${site.slug.charCodeAt(0) % 6}`} aria-hidden="true">
                  {site.slug.charAt(0).toUpperCase()}
                </span>
                <span className="app-name">{site.slug}</span>
              </Link>
              <p className="app-desc">{site.host}</p>
              <div className="app-foot">
                <span>{site.count}</span>
                <CopyButton url={site.url} label={t("copy_link")} copied={t("copied")} />
              </div>
            </article>
          ))
        ) : (
          <Button type="button" className="create-card" disabled={create.isPending} onClick={() => create.mutate()}>
            <span className="plus" aria-hidden="true">
              +
            </span>
            <span className="create-title">{t("create_site")}</span>
            <span className="create-hint">{t("no_sites")}</span>
          </Button>
        )}
      </div>
      <p className="tools-note">
        <span>{t("tools_heading")}</span> <code>create_site</code>, <code>put_file</code>, <code>delete_file</code>, <code>list_files</code>. {t("anon_rejected")}
      </p>
    </div>
  );
}

export function CopyButton({ url, label, copied }: { url: string; label: string; copied: string }) {
  const [text, setText] = useState(label);
  return (
    <Button
      type="button"
      className="copy-btn"
      onClick={() => {
        navigator.clipboard?.writeText(url).then(() => {
          setText(copied);
          window.setTimeout(() => setText(label), 1200);
        });
      }}
    >
      {text}
    </Button>
  );
}
