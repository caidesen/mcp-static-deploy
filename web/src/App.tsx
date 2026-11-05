import { useQuery } from "@tanstack/react-query";
import { Navigate, Route, Routes } from "react-router-dom";
import { api, type Session } from "./api";
import { AuthPage } from "./auth";
import { DetailPage } from "./detail";
import { Shell } from "./shell";
import { SitesPage } from "./sites";

export function useSession() {
  return useQuery({
    queryKey: ["session"],
    queryFn: () => api<Session>("/api/session"),
  });
}

export function App() {
  const session = useSession();
  if (session.isLoading) {
    return <div className="boot" />;
  }
  if (session.isError || !session.data) {
    return <div className="boot" />;
  }
  const data = session.data;
  return (
    <Routes>
      <Route path="/" element={data.authenticated ? <Navigate to="/admin" replace /> : <AuthPage session={data} />} />
      <Route
        path="/admin"
        element={
          data.authenticated ? (
            <Shell session={data}>
              <SitesPage session={data} />
            </Shell>
          ) : (
            <Navigate to="/" replace />
          )
        }
      />
      <Route
        path="/admin/sites/:slug"
        element={
          data.authenticated ? (
            <Shell session={data}>
              <DetailPage session={data} />
            </Shell>
          ) : (
            <Navigate to="/" replace />
          )
        }
      />
      <Route path="*" element={<Navigate to={data.authenticated ? "/admin" : "/"} replace />} />
    </Routes>
  );
}
