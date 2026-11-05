import { Button } from "@base-ui/react/button";
import { Dialog } from "@base-ui/react/dialog";
import { Field } from "@base-ui/react/field";
import { Form } from "@base-ui/react/form";
import { Menu } from "@base-ui/react/menu";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, api, type Session, type Site } from "./api";

export function SitesPage({ session }: { session: Session }) {
  const t = (key: string) => session.messages[key] ?? key;
  const queryClient = useQueryClient();
  const [error, setError] = useState("");
  const [open, setOpen] = useState(false);
  const sites = useQuery({
    queryKey: ["sites"],
    queryFn: () => api<Site[]>("/api/sites"),
  });
  const create = useMutation({
    mutationFn: () => api<Site>("/api/sites", { method: "POST" }),
    onSuccess: () => {
      setError("");
      setOpen(false);
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
        <Dialog.Root open={open} onOpenChange={setOpen}>
          <Dialog.Trigger className="btn btn-primary">+ {t("create_site")}</Dialog.Trigger>
          <Dialog.Portal>
            <Dialog.Backdrop className="dialog-backdrop" />
            <Dialog.Viewport className="dialog-viewport">
              <Dialog.Popup className="dialog-popup">
                <Form
                  className="stack-form"
                  onSubmit={(event) => {
                    event.preventDefault();
                    create.mutate();
                  }}
                >
                  <Dialog.Title className="dialog-title">{t("create_site")}</Dialog.Title>
                  <Field.Root className="field">
                    <Field.Label className="label">{t("sites")}</Field.Label>
                    <Field.Description className="field-hint">{t("site_hint")}</Field.Description>
                  </Field.Root>
                  <Button type="submit" className="btn btn-primary form-submit" disabled={create.isPending}>
                    {t("create_site")}
                  </Button>
                  <Dialog.Close className="btn btn-ghost form-submit">{t("cancel")}</Dialog.Close>
                </Form>
              </Dialog.Popup>
            </Dialog.Viewport>
          </Dialog.Portal>
        </Dialog.Root>
      </div>
      {error ? (
        <p className="error" role="alert">
          {error}
        </p>
      ) : null}
      {sites.data && sites.data.length > 0 ? (
        <div className="app-grid">
          {sites.data.map((site) => (
            <article className="tile" key={site.slug}>
              <Link className="tile-main" to={`/admin/sites/${site.slug}`}>
                <span className={`tile-icon icon-${site.slug.charCodeAt(0) % 6}`} aria-hidden="true">
                  {site.slug.charAt(0).toUpperCase()}
                </span>
                <span className="tile-copy">
                  <span className="tile-name">{site.slug}</span>
                  <span className="tile-host">{site.host}</span>
                </span>
              </Link>
              <div className="tile-foot">
                <span>{site.count}</span>
                <CopyMenu url={site.url} label={t("copy_link")} copied={t("copied")} />
              </div>
            </article>
          ))}
        </div>
      ) : (
        <p className="empty-line">{t("no_sites")}</p>
      )}
      <p className="tools-note">
        <span>{t("tools_heading")}</span> <code>create_site</code>, <code>put_file</code>, <code>delete_file</code>, <code>list_files</code>. {t("anon_rejected")}
      </p>
    </div>
  );
}

export function CopyMenu({ url, label, copied }: { url: string; label: string; copied: string }) {
  const [text, setText] = useState(label);
  return (
    <Menu.Root>
      <Menu.Trigger className="btn btn-ghost tile-menu" aria-label={label}>
        ···
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner className="menu-positioner" side="bottom" align="end" sideOffset={6}>
          <Menu.Popup className="menu-popup">
            <Menu.Item
              className="menu-item"
              onClick={() => {
                navigator.clipboard?.writeText(url).then(() => {
                  setText(copied);
                  window.setTimeout(() => setText(label), 1200);
                });
              }}
            >
              {text}
            </Menu.Item>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}
