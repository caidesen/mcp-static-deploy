import { Button } from "@base-ui/react/button";
import { Dialog } from "@base-ui/react/dialog";
import { Field } from "@base-ui/react/field";
import { Form } from "@base-ui/react/form";
import { Input } from "@base-ui/react/input";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { ApiError, api, type Session, type SiteDetail } from "./api";
import { CopyMenu } from "./sites";

export function DetailPage({ session }: { session: Session }) {
  const t = (key: string) => session.messages[key] ?? key;
  const { slug = "" } = useParams();
  const queryClient = useQueryClient();
  const [error, setError] = useState("");
  const [pendingPath, setPendingPath] = useState<string | null>(null);
  const [fileName, setFileName] = useState("");
  const fileRef = useRef<HTMLInputElement>(null);
  const detail = useQuery({
    queryKey: ["site", slug],
    queryFn: () => api<SiteDetail>(`/api/sites/${slug}`),
  });

  const upload = useMutation({
    mutationFn: (body: FormData) => api<SiteDetail>(`/api/sites/${slug}/files`, { method: "POST", body }),
    onSuccess: (data) => {
      setError("");
      queryClient.setQueryData(["site", slug], data);
      queryClient.invalidateQueries({ queryKey: ["sites"] });
    },
    onError: (err: Error) => setError(err instanceof ApiError ? t(err.message) : t("err_update_file")),
  });

  const remove = useMutation({
    mutationFn: (path: string) => api<SiteDetail>(`/api/sites/${slug}/files?path=${encodeURIComponent(path)}`, { method: "DELETE" }),
    onSuccess: (data) => {
      setError("");
      setPendingPath(null);
      queryClient.setQueryData(["site", slug], data);
      queryClient.invalidateQueries({ queryKey: ["sites"] });
    },
    onError: (err: Error) => setError(err instanceof ApiError ? t(err.message) : t("err_update_file")),
  });

  if (!detail.data) {
    return <div className="studio" />;
  }
  const { site, files, curl } = detail.data;

  return (
    <div className="studio">
      <div className="page-head">
        <div>
          <p className="crumb">
            <Link to="/admin">{t("all_sites")}</Link> <span aria-hidden="true">/</span> {site.slug}
          </p>
          <h1>{site.slug}</h1>
        </div>
      </div>
      <section className="metrics">
        <article className="metric">
          <span className="metric-label">{t("files_label")}</span>
          <strong>{files.length}</strong>
        </article>
        <article className="metric">
          <span className="metric-label">{t("public_url")}</span>
          <div className="metric-row">
            <a className="metric-host" href={site.url}>
              {site.host}
            </a>
            <CopyMenu url={site.url} label={t("copy_link")} copied={t("copied")} />
          </div>
        </article>
      </section>
      <details className="secondary">
        <summary>{t("local_check")}</summary>
        <code>{curl}</code>
      </details>
      {error ? (
        <p className="error" role="alert">
          {error}
        </p>
      ) : null}
      <Form
        className="card"
        onSubmit={(event) => {
          event.preventDefault();
          const data = new FormData(event.currentTarget);
          const file = data.get("file");
          if (!(file instanceof File) || file.name === "") {
            setError(t("err_choose_file"));
            return;
          }
          upload.mutate(data);
          event.currentTarget.reset();
          setFileName("");
        }}
      >
        <h2>{t("upload")}</h2>
        <Field.Root name="file" className="field">
          <Field.Label className="label" htmlFor="file">
            {t("file")}
          </Field.Label>
          <div className="file-field">
            <Input
              id="file"
              name="file"
              type="file"
              required
              className="file-native"
              ref={fileRef}
              onChange={(event) => setFileName(event.currentTarget.files?.[0]?.name ?? "")}
            />
            <Button type="button" className="btn file-picker" onClick={() => fileRef.current?.click()}>
              <span>{t("choose_file")}</span>
              {fileName ? <span className="file-name">{fileName}</span> : null}
            </Button>
          </div>
        </Field.Root>
        <Field.Root name="path" className="field">
          <Field.Label className="label">{t("path_label")}</Field.Label>
          <Input id="path" name="path" type="text" placeholder="index.html" autoComplete="off" className="text-input" />
        </Field.Root>
        <Button type="submit" className="btn btn-primary form-submit" disabled={upload.isPending}>
          {t("upload")}
        </Button>
      </Form>
      <div className="card table-card">
        <div className="card-head">
          <h2>{t("files_heading")}</h2>
        </div>
        {files.length > 0 ? (
          <table>
            <thead>
              <tr>
                <th>{t("col_name")}</th>
                <th>{t("col_size")}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {files.map((file) => (
                <tr key={file.path}>
                  <td>
                    <code>{file.path}</code>
                  </td>
                  <td className="muted">
                    {file.size} {t("bytes")}
                  </td>
                  <td className="col-action">
                    <Button type="button" className="btn btn-danger" onClick={() => setPendingPath(file.path)}>
                      {t("delete")}
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <p className="empty-files">
            {t("no_files_before")} <code>index.html</code> {t("no_files_after")}
        </p>
      )}
      </div>
      <Dialog.Root open={pendingPath !== null} onOpenChange={(open) => { if (!open) setPendingPath(null); }}>
        <Dialog.Portal>
          <Dialog.Backdrop className="dialog-backdrop" />
          <Dialog.Viewport className="dialog-viewport">
            <Dialog.Popup className="dialog-popup">
              <Dialog.Title className="dialog-title">{t("delete")}</Dialog.Title>
              <Dialog.Description className="dialog-copy">{pendingPath}</Dialog.Description>
              <div className="dialog-actions">
                <Dialog.Close className="btn btn-ghost">{t("cancel")}</Dialog.Close>
                <Button
                  type="button"
                  className="btn btn-danger"
                  disabled={remove.isPending || !pendingPath}
                  onClick={() => pendingPath && remove.mutate(pendingPath)}
                >
                  {t("delete")}
                </Button>
              </div>
            </Dialog.Popup>
          </Dialog.Viewport>
        </Dialog.Portal>
      </Dialog.Root>
    </div>
  );
}
