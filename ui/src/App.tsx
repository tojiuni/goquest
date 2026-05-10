import { Refine, Authenticated } from "@refinedev/core";
import {
  RefineThemes,
  ThemedLayoutV2,
  ThemedSiderV2,
  useNotificationProvider,
} from "@refinedev/antd";
import {
  BrowserRouter,
  Route,
  Routes,
  Outlet,
} from "react-router-dom";
import routerBindings, {
  NavigateToResource,
  CatchAllNavigate,
  UnsavedChangesNotifier,
  DocumentTitleHandler,
} from "@refinedev/react-router-v6";
import { ConfigProvider, App as AntApp } from "antd";
import {
  UnorderedListOutlined,
  ControlOutlined,
  TeamOutlined,
  FolderOutlined,
} from "@ant-design/icons";

import "@refinedev/antd/dist/reset.css";

import { authProvider } from "@/authProvider";
import dataProvider from "@/dataProvider";
import { WorkspaceProvider } from "@/contexts/WorkspaceContext";
import { WorkspaceSelector } from "@/components/WorkspaceSelector";

import { TicketList } from "@/pages/tickets/list";
import { TicketShow } from "@/pages/tickets/show";
import { TicketCreate } from "@/pages/tickets/create";
import { TicketEdit } from "@/pages/tickets/edit";
import { AuthCallback } from "@/pages/auth/callback";
import { WorkspaceList } from "@/pages/workspaces/list";
import { WorkspaceShow } from "@/pages/workspaces/show";
import { ProjectList } from "@/pages/projects/list";
import { ProjectShow } from "@/pages/projects/show";

const LoginPage = () => (
  <div
    style={{
      display: "flex",
      flexDirection: "column",
      alignItems: "center",
      justifyContent: "center",
      height: "100vh",
      gap: 24,
      background: "#f5f5f5",
    }}
  >
    <ControlOutlined style={{ fontSize: 48, color: "#1677ff" }} />
    <h1 style={{ margin: 0 }}>Goquest</h1>
    <p style={{ color: "#666" }}>AI-agent ticket management</p>
    <button
      style={{
        background: "#1677ff",
        color: "#fff",
        border: "none",
        borderRadius: 6,
        padding: "10px 32px",
        fontSize: 16,
        cursor: "pointer",
      }}
      onClick={() => authProvider.login({})}
    >
      Sign in with ZITADEL
    </button>
  </div>
);

export const App = () => (
  <BrowserRouter>
    <ConfigProvider theme={RefineThemes.Blue}>
      <AntApp>
        <WorkspaceProvider>
          <Refine
            routerProvider={routerBindings}
            dataProvider={dataProvider}
            authProvider={authProvider}
            notificationProvider={useNotificationProvider}
            resources={[
              {
                name: "tickets",
                list: "/tickets",
                show: "/tickets/:id",
                create: "/tickets/new",
                edit: "/tickets/:id/edit",
                meta: {
                  label: "Tickets",
                  icon: <UnorderedListOutlined />,
                },
              },
              {
                name: "workspaces",
                list: "/workspaces",
                show: "/workspaces/:id",
                meta: {
                  label: "Workspaces",
                  icon: <TeamOutlined />,
                },
              },
              {
                name: "projects",
                list: "/projects",
                show: "/projects/:id",
                meta: {
                  label: "Projects",
                  icon: <FolderOutlined />,
                },
              },
            ]}
            options={{
              syncWithLocation: true,
              warnWhenUnsavedChanges: true,
            }}
          >
            <Routes>
              {/* Auth */}
              <Route path="/auth/callback" element={<AuthCallback />} />
              <Route path="/auth/silent" element={<div />} />
              <Route path="/login" element={<LoginPage />} />

              {/* Protected */}
              <Route
                element={
                  <Authenticated key="authenticated" fallback={<CatchAllNavigate to="/login" />}>
                    <ThemedLayoutV2
                      Sider={() => (
                        <ThemedSiderV2
                          Title={() => (
                            <div style={{ padding: "0 8px" }}>
                              <div style={{ fontWeight: 700, fontSize: 18, marginBottom: 8 }}>
                                <ControlOutlined /> Goquest
                              </div>
                              <WorkspaceSelector />
                            </div>
                          )}
                        />
                      )}
                    >
                      <Outlet />
                    </ThemedLayoutV2>
                  </Authenticated>
                }
              >
                <Route index element={<NavigateToResource resource="tickets" />} />

                {/* Tickets */}
                <Route path="/tickets">
                  <Route index element={<TicketList />} />
                  <Route path="new" element={<TicketCreate />} />
                  <Route path=":id" element={<TicketShow />} />
                  <Route path=":id/edit" element={<TicketEdit />} />
                </Route>

                {/* Workspaces */}
                <Route path="/workspaces">
                  <Route index element={<WorkspaceList />} />
                  <Route path=":id" element={<WorkspaceShow />} />
                </Route>

                {/* Projects */}
                <Route path="/projects">
                  <Route index element={<ProjectList />} />
                  <Route path=":id" element={<ProjectShow />} />
                </Route>

                <Route path="*" element={<NavigateToResource resource="tickets" />} />
              </Route>
            </Routes>

            <UnsavedChangesNotifier />
            <DocumentTitleHandler />
          </Refine>
        </WorkspaceProvider>
      </AntApp>
    </ConfigProvider>
  </BrowserRouter>
);
