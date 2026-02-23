import { useEffect } from "react";
import { Routes, Route, Navigate } from "react-router-dom";
import { useSessionStore } from "@/stores/sessionStore";
import { Login } from "@/components/Login";
import { Chat } from "@/components/Chat";

export function App() {
  const { isAuthenticated, init } = useSessionStore();
  useEffect(() => { init(); }, [init]);
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/*" element={isAuthenticated ? <Chat /> : <Navigate to="/login" replace />} />
    </Routes>
  );
}
