import React, { createContext, useContext, useEffect, useState, useCallback } from "react";
import { workspaceService } from "@/services/api";
import type { Workspace } from "@/config";

const STORAGE_KEY = "goquest:workspace_id";

interface WorkspaceContextValue {
  workspaces: Workspace[];
  currentWorkspace: Workspace | null;
  loading: boolean;
  setCurrentWorkspace: (ws: Workspace) => void;
  refresh: () => Promise<void>;
}

const WorkspaceContext = createContext<WorkspaceContextValue>({
  workspaces: [],
  currentWorkspace: null,
  loading: true,
  setCurrentWorkspace: () => {},
  refresh: async () => {},
});

export const WorkspaceProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [currentWorkspace, setCurrentWs] = useState<Workspace | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const { data } = await workspaceService.listWorkspaces();
      const list = data.workspaces ?? [];
      setWorkspaces(list);

      const savedId = localStorage.getItem(STORAGE_KEY);
      const saved = list.find((w) => w.id === savedId);
      if (saved) {
        setCurrentWs(saved);
      } else if (list.length > 0) {
        setCurrentWs(list[0]);
        localStorage.setItem(STORAGE_KEY, list[0].id);
      }
    } catch {
      // not logged in yet or API unavailable
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const setCurrentWorkspace = useCallback((ws: Workspace) => {
    setCurrentWs(ws);
    localStorage.setItem(STORAGE_KEY, ws.id);
  }, []);

  return (
    <WorkspaceContext.Provider value={{ workspaces, currentWorkspace, loading, setCurrentWorkspace, refresh: load }}>
      {children}
    </WorkspaceContext.Provider>
  );
};

export const useWorkspace = () => useContext(WorkspaceContext);
