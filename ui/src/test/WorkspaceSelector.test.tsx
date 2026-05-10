import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { WorkspaceSelector } from "@/components/WorkspaceSelector";
import * as WorkspaceContextModule from "@/contexts/WorkspaceContext";
import type { Workspace } from "@/config";

vi.mock("@/contexts/WorkspaceContext", async (importOriginal) => {
  const actual = await importOriginal<typeof WorkspaceContextModule>();
  return { ...actual, useWorkspace: vi.fn() };
});

const ws1: Workspace = { id: "1", name: "Prod", slug: "prod", type: "shared", owner_id: "u1", created_at: "", updated_at: "" };

describe("WorkspaceSelector", () => {
  it("shows spinner when loading", () => {
    vi.mocked(WorkspaceContextModule.useWorkspace).mockReturnValue({
      workspaces: [],
      currentWorkspace: null,
      loading: true,
      setCurrentWorkspace: vi.fn(),
      refresh: vi.fn(),
    });

    const { container } = render(<WorkspaceSelector />);
    expect(container.querySelector(".ant-spin")).toBeTruthy();
  });

  it("shows 'No workspace' when list is empty", () => {
    vi.mocked(WorkspaceContextModule.useWorkspace).mockReturnValue({
      workspaces: [],
      currentWorkspace: null,
      loading: false,
      setCurrentWorkspace: vi.fn(),
      refresh: vi.fn(),
    });

    render(<WorkspaceSelector />);
    expect(screen.getByText("No workspace")).toBeTruthy();
  });

  it("renders select with workspace options when loaded", () => {
    vi.mocked(WorkspaceContextModule.useWorkspace).mockReturnValue({
      workspaces: [ws1],
      currentWorkspace: ws1,
      loading: false,
      setCurrentWorkspace: vi.fn(),
      refresh: vi.fn(),
    });

    const { container } = render(<WorkspaceSelector />);
    expect(container.querySelector(".ant-select")).toBeTruthy();
  });
});
