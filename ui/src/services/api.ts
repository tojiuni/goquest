import axios from "axios";
import type { AxiosInstance, AxiosRequestConfig } from "axios";
import { userManager } from "@/authProvider";
import { API_BASE_URL } from "@/config";
import type {
  Workspace,
  WorkspaceMember,
  WorkspaceInvitation,
  WorkspaceRole,
  Project,
  ProjectMember,
  ProjectRole,
  Tag,
  Ticket,
  Attempt,
  APIKey,
  Template,
  XPStatus,
  XPLevel,
} from "@/config";

function makeClient(baseURL: string): AxiosInstance {
  const client = axios.create({ baseURL });
  client.interceptors.request.use(async (config) => {
    const user = await userManager.getUser();
    if (user?.access_token) {
      config.headers.Authorization = `Bearer ${user.access_token}`;
    }
    return config;
  });
  client.interceptors.response.use(
    (r) => r,
    (err) => {
      if (err.response?.status === 401) {
        userManager.signinRedirect();
      }
      return Promise.reject(err);
    }
  );
  return client;
}

// Keep the abstract base for backward compatibility
abstract class APIService {
  protected readonly client: AxiosInstance;

  constructor(baseURL: string) {
    this.client = makeClient(baseURL);
  }

  get<T = unknown>(url: string, config?: AxiosRequestConfig) {
    return this.client.get<T>(url, config);
  }
  post<T = unknown>(url: string, data?: unknown, config?: AxiosRequestConfig) {
    return this.client.post<T>(url, data, config);
  }
  patch<T = unknown>(url: string, data?: unknown, config?: AxiosRequestConfig) {
    return this.client.patch<T>(url, data, config);
  }
  del<T = unknown>(url: string, config?: AxiosRequestConfig) {
    return this.client.delete<T>(url, config);
  }
}

class TicketService extends APIService {
  constructor() {
    super(API_BASE_URL);
  }

  listTickets(params?: Record<string, unknown>) {
    return this.client.get<{ tickets: Ticket[]; total: number }>("/tickets", { params });
  }

  getTicket(id: string) {
    return this.client.get<Ticket>(`/tickets/${id}`);
  }

  createTicket(data: unknown) {
    return this.client.post<Ticket>("/tickets", data);
  }

  updateTicket(id: string, data: unknown) {
    return this.client.patch<Ticket>(`/tickets/${id}`, data);
  }

  cancelTicket(id: string) {
    return this.client.delete<Ticket>(`/tickets/${id}`);
  }

  listSubtasks(ticketId: string) {
    return this.client.get<{ subtasks: Ticket[] }>(`/tickets/${ticketId}/subtasks`);
  }

  submitAttempt(ticketId: string, answer: string) {
    return this.client.post<Attempt>(`/tickets/${ticketId}/attempts`, { answer });
  }

  listAttempts(ticketId: string) {
    return this.client.get<{ attempts: Attempt[] }>(`/tickets/${ticketId}/attempts`);
  }
}

class WorkspaceService extends APIService {
  constructor() {
    super(API_BASE_URL);
  }

  listWorkspaces() {
    return this.client.get<{ workspaces: Workspace[] }>("/workspaces");
  }

  getWorkspace(id: string) {
    return this.client.get<Workspace>(`/workspaces/${id}`);
  }

  createWorkspace(data: { name: string; type?: string }) {
    return this.client.post<Workspace>("/workspaces", data);
  }

  updateWorkspace(id: string, data: { name?: string }) {
    return this.client.patch<Workspace>(`/workspaces/${id}`, data);
  }

  deleteWorkspace(id: string) {
    return this.client.delete(`/workspaces/${id}`);
  }

  listMembers(workspaceId: string) {
    return this.client.get<{ members: WorkspaceMember[] }>(`/workspaces/${workspaceId}/members`);
  }

  setMemberRole(workspaceId: string, userId: string, role: WorkspaceRole) {
    return this.client.patch(`/workspaces/${workspaceId}/members/${userId}/role`, { role });
  }

  removeMember(workspaceId: string, userId: string) {
    return this.client.delete(`/workspaces/${workspaceId}/members/${userId}`);
  }

  listInvitations(workspaceId: string) {
    return this.client.get<{ invitations: WorkspaceInvitation[] }>(`/workspaces/${workspaceId}/invitations`);
  }

  createInvitation(workspaceId: string, data: { invitee_email: string; role: WorkspaceRole }) {
    return this.client.post<WorkspaceInvitation>(`/workspaces/${workspaceId}/invitations`, data);
  }

  deleteInvitation(workspaceId: string, invitationId: string) {
    return this.client.delete(`/workspaces/${workspaceId}/invitations/${invitationId}`);
  }

  acceptInvitation(token: string) {
    return this.client.post(`/invitations/${token}/accept`);
  }
}

class ProjectService extends APIService {
  constructor() {
    super(API_BASE_URL);
  }

  listProjects(workspaceId: string) {
    return this.client.get<{ projects: Project[] }>(`/workspaces/${workspaceId}/projects`);
  }

  getProject(workspaceId: string, projectId: string) {
    return this.client.get<Project>(`/workspaces/${workspaceId}/projects/${projectId}`);
  }

  createProject(workspaceId: string, data: { name: string; description?: string }) {
    return this.client.post<Project>(`/workspaces/${workspaceId}/projects`, data);
  }

  updateProject(workspaceId: string, projectId: string, data: { name?: string; description?: string }) {
    return this.client.patch<Project>(`/workspaces/${workspaceId}/projects/${projectId}`, data);
  }

  deleteProject(workspaceId: string, projectId: string) {
    return this.client.delete(`/workspaces/${workspaceId}/projects/${projectId}`);
  }

  listMembers(workspaceId: string, projectId: string) {
    return this.client.get<{ members: ProjectMember[] }>(
      `/workspaces/${workspaceId}/projects/${projectId}/members`
    );
  }

  addMember(workspaceId: string, projectId: string, userId: string, role: ProjectRole) {
    return this.client.post(`/workspaces/${workspaceId}/projects/${projectId}/members`, { user_id: userId, role });
  }

  setMemberRole(workspaceId: string, projectId: string, userId: string, role: ProjectRole) {
    return this.client.patch(`/workspaces/${workspaceId}/projects/${projectId}/members/${userId}/role`, { role });
  }

  removeMember(workspaceId: string, projectId: string, userId: string) {
    return this.client.delete(`/workspaces/${workspaceId}/projects/${projectId}/members/${userId}`);
  }
}

class TagService extends APIService {
  constructor() {
    super(API_BASE_URL);
  }

  listTags(workspaceId: string) {
    return this.client.get<{ tags: Tag[] }>(`/workspaces/${workspaceId}/tags`);
  }

  createTag(workspaceId: string, data: { name: string; color?: string }) {
    return this.client.post<Tag>(`/workspaces/${workspaceId}/tags`, data);
  }

  updateTag(workspaceId: string, tagId: string, data: { name?: string; color?: string }) {
    return this.client.patch<Tag>(`/workspaces/${workspaceId}/tags/${tagId}`, data);
  }

  deleteTag(workspaceId: string, tagId: string) {
    return this.client.delete(`/workspaces/${workspaceId}/tags/${tagId}`);
  }
}

class XPService extends APIService {
  constructor() {
    super(API_BASE_URL);
  }

  getMyXP(workspaceId: string) {
    return this.client.get<XPStatus>(`/workspaces/${workspaceId}/xp/me`);
  }

  listLevels() {
    return this.client.get<{ levels: XPLevel[] }>("/xp/levels");
  }
}

class APIKeyService extends APIService {
  constructor() {
    super(API_BASE_URL);
  }

  list(wid: string, pid: string) {
    return this.client.get<{ api_keys: APIKey[] }>(
      `/workspaces/${wid}/projects/${pid}/api-keys`
    );
  }

  create(wid: string, pid: string, name: string) {
    return this.client.post<APIKey & { key: string }>(
      `/workspaces/${wid}/projects/${pid}/api-keys`,
      { name }
    );
  }

  delete(wid: string, pid: string, kid: string) {
    return this.client.delete(`/workspaces/${wid}/projects/${pid}/api-keys/${kid}`);
  }
}

class TemplateService extends APIService {
  constructor() {
    super(API_BASE_URL);
  }

  list(wid: string, pid: string) {
    return this.client.get<{ templates: Template[] }>(
      `/workspaces/${wid}/projects/${pid}/templates`
    );
  }

  create(wid: string, pid: string, data: Partial<Template>) {
    return this.client.post<Template>(
      `/workspaces/${wid}/projects/${pid}/templates`,
      data
    );
  }

  delete(wid: string, pid: string, tid: string) {
    return this.client.delete(`/workspaces/${wid}/projects/${pid}/templates/${tid}`);
  }

  instantiate(
    wid: string,
    pid: string,
    tid: string,
    overrides: Record<string, unknown>
  ) {
    return this.client.post<Ticket>(
      `/workspaces/${wid}/projects/${pid}/templates/${tid}/instantiate`,
      overrides
    );
  }
}

export const ticketService = new TicketService();
export const workspaceService = new WorkspaceService();
export const projectService = new ProjectService();
export const tagService = new TagService();
export const xpService = new XPService();
export const apiKeyService = new APIKeyService();
export const templateService = new TemplateService();
export { APIService };
