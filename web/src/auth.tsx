import { Button } from "@base-ui/react/button";
import { Field } from "@base-ui/react/field";
import { Form } from "@base-ui/react/form";
import { Input } from "@base-ui/react/input";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { ApiError, api, type Session } from "./api";
import { LangSwitch } from "./shell";

export function AuthPage({ session }: { session: Session }) {
  const t = (key: string) => session.messages[key] ?? key;
  const [params] = useSearchParams();
  const next = params.get("next") ?? "";
  const queryClient = useQueryClient();
  const [error, setError] = useState("");
  const signup = session.needsSetup;

  const submit = useMutation({
    mutationFn: (body: { email: string; password: string; setupToken?: string }) =>
      api<{ ok: boolean; next: string }>(signup ? "/api/signup" : "/api/login", {
        method: "POST",
        body: JSON.stringify({ ...body, next }),
      }),
    onSuccess: (data) => {
      window.location.assign(data.next || "/admin");
    },
    onError: (err: Error) => {
      queryClient.invalidateQueries({ queryKey: ["session"] });
      setError(err instanceof ApiError ? t(err.message) : t("err_sign_in_failed"));
    },
  });

  return (
    <div className="auth-shell">
      <aside className="auth-aside">
        <div className="auth-hero">
          <span className="auth-hero-mark" aria-hidden="true" />
          <p>{t("brand")}</p>
        </div>
      </aside>
      <section className="auth-main">
        <LangSwitch lang={session.lang} label={t("language")} />
        <div className="auth-block">
          <a className="logo" href="/">
            <LogoMark />
            <span>{t("brand")}</span>
          </a>
          <h1>{signup ? t("create_account") : t("sign_in")}</h1>
          {error ? (
            <p className="error" role="alert">
              {error}
            </p>
          ) : null}
          <Form
            onSubmit={(event) => {
              event.preventDefault();
              const data = new FormData(event.currentTarget);
              setError("");
              submit.mutate({
                email: String(data.get("email") ?? ""),
                password: String(data.get("password") ?? ""),
                setupToken: signup ? String(data.get("setup_token") ?? "") : undefined,
              });
            }}
          >
            <Field.Root name="email" className="field">
              <Field.Label className="label">{t("email")}</Field.Label>
              <Input id="email" name="email" type="email" autoComplete="username" required className="text-input" />
            </Field.Root>
            <Field.Root name="password" className="field">
              <Field.Label className="label">{t("password")}</Field.Label>
              <Input
                id="password"
                name="password"
                type="password"
                autoComplete={signup ? "new-password" : "current-password"}
                minLength={signup ? 12 : undefined}
                required
                className="text-input"
              />
            </Field.Root>
            {signup ? (
              <Field.Root name="setup_token" className="field">
                <Field.Label className="label">{t("setup_token")}</Field.Label>
                <Input id="setup_token" name="setup_token" type="password" autoComplete="off" required className="text-input" />
              </Field.Root>
            ) : null}
            <Button type="submit" className="btn btn-primary form-submit" disabled={submit.isPending}>
              {signup ? t("create_account_btn") : t("sign_in")}
            </Button>
          </Form>
        </div>
      </section>
    </div>
  );
}

export function LogoMark() {
  return (
    <svg className="mark" width="28" height="28" viewBox="0 0 28 28" aria-hidden="true">
      <rect width="28" height="28" rx="8" fill="#16a34a" />
      <rect x="6.5" y="6.5" width="6.2" height="6.2" rx="1.4" fill="#fff" />
      <rect x="15.3" y="6.5" width="6.2" height="6.2" rx="1.4" fill="#fff" opacity=".88" />
      <rect x="6.5" y="15.3" width="6.2" height="6.2" rx="1.4" fill="#fff" opacity=".72" />
      <rect x="15.3" y="15.3" width="6.2" height="6.2" rx="1.4" fill="#fff" opacity=".5" />
    </svg>
  );
}
