import { useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { Spin } from "antd";
import { userManager } from "@/authProvider";

export const AuthCallback = () => {
  const navigate = useNavigate();

  useEffect(() => {
    userManager
      .signinRedirectCallback()
      .then(() => navigate("/", { replace: true }))
      .catch(() => navigate("/login", { replace: true }));
  }, [navigate]);

  return (
    <div style={{ display: "flex", justifyContent: "center", alignItems: "center", height: "100vh" }}>
      <Spin size="large" tip="Signing in…" />
    </div>
  );
};
