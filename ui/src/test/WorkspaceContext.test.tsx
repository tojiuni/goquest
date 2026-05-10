import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, act, waitFor } from "@testing-library/react";
import { WorkspaceProvider, useWorkspace } from "@/contexts/WorkspaceContext";
import type { Workspace } from "@/config";

// Mock the API service
vi.mock("@/services/api", () => ({
  workspaceService: {
    listWorkspaces: vi.fn(),
  },
}));

// Mock authProvider so userManager doesn't need real OIDC
vi.mock("@/authProvider", () => ({
  userManager: { getUser: vi.fn().mockResolvedValue(null) },
}));

const mockWorkspaces: Workspace[] = [
  { id: "ws-1", name: "Alpha", slug: "alpha", type: "shared", owner_id: "u1", created_at: "", updated_at: "" },
  { id: "ws-2", name: "Beta", slug: "beta", type: "personal", owner_id: "u2", created_at: "", updated_at: "" },
];

const ConsumerComponent = () => {
  const { currentWorkspace, workspaces } = useWorkspace();
  return (
    <div>
      <span data-testid="count">{workspaces.length}</span>
      <span data-testid="current">{currentWorkspace?.name ?? "none"}</span>
    </div>
  );
};

describe("WorkspaceProvider", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
  });

  it("loads workspaces and selects first by default", async () => {
    const { workspaceService } = await import("@/services/api");
    vi.mocked(workspaceService.listWorkspaces).mockResolvedValue({
      data: { workspaces: mockWorkspaces },
    } as never);

    render(
      <WorkspaceProvider>
        <ConsumerComponent />
      </WorkspaceProvider>
    );

    await waitFor(() => {
      expect(screen.getByTestId("count").textContent).toBe("2");
      expect(screen.getByTestId("current").textContent).toBe("Alpha");
    });
  });

  it("restores saved workspace from localStorage", async () => {
    localStorage.setItem("goquest:workspace_id", "ws-2");
    const { workspaceService } = await import("@/services/api");
    vi.mocked(workspaceService.listWorkspaces).mockResolvedValue({
      data: { workspaces: mockWorkspaces },
    } as never);

    render(
      <WorkspaceProvider>
        <ConsumerComponent />
      </WorkspaceProvider>
    );

    await waitFor(() => {
      expect(screen.getByTestId("current").textContent).toBe("Beta");
    });
  });

  it("persists workspace selection to localStorage", async () => {
    const { workspaceService } = await import("@/services/api");
    vi.mocked(workspaceService.listWorkspaces).mockResolvedValue({
      data: { workspaces: mockWorkspaces },
    } as never);

    const SwitcherComponent = () => {
      const { setCurrentWorkspace } = useWorkspace();
      return (
        <button onClick={() => setCurrentWorkspace(mockWorkspaces[1])}>
          switch
        </button>
      );
    };

    render(
      <WorkspaceProvider>
        <SwitcherComponent />
      </WorkspaceProvider>
    );

    await act(async () => {
      screen.getByText("switch").click();
    });

    expect(localStorage.getItem("goquest:workspace_id")).toBe("ws-2");
  });

  it("handles API failure gracefully", async () => {
    const { workspaceService } = await import("@/services/api");
    vi.mocked(workspaceService.listWorkspaces).mockRejectedValue(new Error("network error"));

    render(
      <WorkspaceProvider>
        <ConsumerComponent />
      </WorkspaceProvider>
    );

    await waitFor(() => {
      expect(screen.getByTestId("count").textContent).toBe("0");
      expect(screen.getByTestId("current").textContent).toBe("none");
    });
  });
});
