import type { DataProvider } from "@refinedev/core";
import { ticketService, workspaceService, projectService, tagService } from "@/services/api";

const STORAGE_KEY = "goquest:workspace_id";
const getWorkspaceId = () => localStorage.getItem(STORAGE_KEY) ?? "";

const dataProvider: DataProvider = {
  getList: async ({ resource, pagination, filters, meta }) => {
    const page = pagination?.current ?? 1;
    const pageSize = pagination?.pageSize ?? 20;

    if (resource === "workspaces") {
      const { data } = await workspaceService.listWorkspaces();
      const list = data.workspaces ?? [];
      return { data: list as never[], total: list.length };
    }

    if (resource === "projects") {
      const wid = (meta?.workspaceId as string | undefined) ?? getWorkspaceId();
      const { data } = await projectService.listProjects(wid);
      const list = data.projects ?? [];
      return { data: list as never[], total: list.length };
    }

    if (resource === "tags") {
      const wid = (meta?.workspaceId as string | undefined) ?? getWorkspaceId();
      const { data } = await tagService.listTags(wid);
      const list = data.tags ?? [];
      return { data: list as never[], total: list.length };
    }

    // default: tickets
    const params: Record<string, unknown> = {
      limit: pageSize,
      offset: (page - 1) * pageSize,
    };
    filters?.forEach((f) => {
      if ("field" in f && f.operator === "eq" && f.value !== undefined && f.value !== "") {
        params[f.field] = f.value;
      }
    });

    const { data } = await ticketService.listTickets(params);
    return { data: data.tickets as never[], total: data.total };
  },

  getOne: async ({ resource, id, meta }) => {
    if (resource === "workspaces") {
      const { data } = await workspaceService.getWorkspace(String(id));
      return { data: data as never };
    }

    if (resource === "projects") {
      const wid = (meta?.workspaceId as string | undefined) ?? getWorkspaceId();
      const { data } = await projectService.getProject(wid, String(id));
      return { data: data as never };
    }

    const { data } = await ticketService.getTicket(String(id));
    return { data: data as never };
  },

  create: async ({ resource, variables, meta }) => {
    if (resource === "workspaces") {
      const { data } = await workspaceService.createWorkspace(variables as { name: string; type?: string });
      return { data: data as never };
    }

    if (resource === "projects") {
      const wid = (meta?.workspaceId as string | undefined) ?? getWorkspaceId();
      const { data } = await projectService.createProject(wid, variables as { name: string; description?: string });
      return { data: data as never };
    }

    if (resource === "tags") {
      const wid = (meta?.workspaceId as string | undefined) ?? getWorkspaceId();
      const { data } = await tagService.createTag(wid, variables as { name: string; color?: string });
      return { data: data as never };
    }

    const { data } = await ticketService.createTicket(variables);
    return { data: data as never };
  },

  update: async ({ resource, id, variables, meta }) => {
    if (resource === "workspaces") {
      const { data } = await workspaceService.updateWorkspace(String(id), variables as { name?: string });
      return { data: data as never };
    }

    if (resource === "projects") {
      const wid = (meta?.workspaceId as string | undefined) ?? getWorkspaceId();
      const { data } = await projectService.updateProject(wid, String(id), variables as { name?: string; description?: string });
      return { data: data as never };
    }

    if (resource === "tags") {
      const wid = (meta?.workspaceId as string | undefined) ?? getWorkspaceId();
      const { data } = await tagService.updateTag(wid, String(id), variables as { name?: string; color?: string });
      return { data: data as never };
    }

    const { data } = await ticketService.updateTicket(String(id), variables);
    return { data: data as never };
  },

  deleteOne: async ({ resource, id, meta }) => {
    if (resource === "workspaces") {
      await workspaceService.deleteWorkspace(String(id));
      return { data: { id } as never };
    }

    if (resource === "projects") {
      const wid = (meta?.workspaceId as string | undefined) ?? getWorkspaceId();
      await projectService.deleteProject(wid, String(id));
      return { data: { id } as never };
    }

    if (resource === "tags") {
      const wid = (meta?.workspaceId as string | undefined) ?? getWorkspaceId();
      await tagService.deleteTag(wid, String(id));
      return { data: { id } as never };
    }

    const { data } = await ticketService.cancelTicket(String(id));
    return { data: data as never };
  },

  getApiUrl: () => "/api/v1",
};

export default dataProvider;
