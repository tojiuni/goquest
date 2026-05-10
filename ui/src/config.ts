export const ZITADEL_AUTHORITY =
  import.meta.env.VITE_ZITADEL_AUTHORITY ?? "https://auth.toji.homes";

export const ZITADEL_CLIENT_ID =
  import.meta.env.VITE_ZITADEL_CLIENT_ID ?? "";

export const API_BASE_URL =
  import.meta.env.VITE_API_URL
    ? `${import.meta.env.VITE_API_URL}/api/v1`
    : "/api/v1";

export const TICKET_STATUSES = [
  "pending",
  "assigned",
  "in_progress",
  "completed",
  "failed",
  "cancelled",
] as const;

export const TICKET_TYPES = [
  "task",
  "bug",
  "feature",
  "incident",
  "request",
] as const;

export const PRIORITIES = ["urgent", "high", "normal", "low"] as const;

export const DOMAINS = [
  "infra",
  "data",
  "code",
  "monitor",
  "knowledge",
  "pm",
] as const;

export type TicketStatus = (typeof TICKET_STATUSES)[number];
export type TicketType = (typeof TICKET_TYPES)[number];
export type Priority = (typeof PRIORITIES)[number];
export type Domain = (typeof DOMAINS)[number];

export const COMPLETION_MODES = ["manual", "answer", "agent", "subtask"] as const;
export const VISIBILITIES = ["project", "restricted"] as const;
export const WORKSPACE_ROLES = ["owner", "admin", "member", "viewer"] as const;
export const PROJECT_ROLES = ["admin", "member", "viewer"] as const;
export const WORKSPACE_TYPES = ["personal", "shared"] as const;
export const PROJECT_STATUSES = ["active", "archived"] as const;

export type CompletionMode = (typeof COMPLETION_MODES)[number];
export type Visibility = (typeof VISIBILITIES)[number];
export type WorkspaceRole = (typeof WORKSPACE_ROLES)[number];
export type ProjectRole = (typeof PROJECT_ROLES)[number];

export interface Workspace {
  id: string;
  name: string;
  slug: string;
  type: "personal" | "shared";
  owner_id: string;
  created_at: string;
  updated_at: string;
}

export interface WorkspaceMember {
  workspace_id: string;
  user_id: string;
  role: WorkspaceRole;
  joined_at: string;
}

export interface WorkspaceInvitation {
  id: string;
  workspace_id: string;
  invitee_email: string;
  role: WorkspaceRole;
  token: string;
  expires_at: string;
  used: boolean;
  created_at: string;
}

export interface Project {
  id: string;
  workspace_id: string;
  name: string;
  description: string;
  status: "active" | "archived";
  owner_id: string;
  created_at: string;
  updated_at: string;
}

export interface ProjectMember {
  project_id: string;
  user_id: string;
  role: ProjectRole;
  joined_at: string;
}

export interface Tag {
  id: string;
  workspace_id: string;
  name: string;
  color: string;
  created_by: string;
  created_at: string;
}

export interface Ticket {
  id: string;
  title: string;
  description: string;
  type: string;
  status: TicketStatus;
  priority: Priority;
  assignee_type: string;
  assignee_id: string;
  requester_id: string;
  source_bot?: string;
  domain?: string;
  agent_context?: Record<string, unknown>;
  vikunja_task_id?: number;
  metadata?: Record<string, unknown>;
  project_id?: string;
  parent_id?: string;
  visibility: Visibility;
  completion_mode: CompletionMode;
  completion_spec?: Record<string, unknown>;
  reward_xp: number;
  due_at?: string;
  callback_url?: string;
  tags?: Tag[];
  created_at: string;
  updated_at: string;
  completed_at?: string;
}

export interface Attempt {
  id: string;
  ticket_id: string;
  user_id: string;
  answer: string;
  is_correct?: boolean;
  ai_feedback?: string;
  attempted_at: string;
}

export interface APIKey {
  id: string;
  project_id: string;
  name: string;
  created_by: string;
  last_used_at?: string;
  created_at: string;
}

export interface Template {
  id: string;
  project_id: string;
  name: string;
  title?: string;
  description?: string;
  type?: string;
  priority?: string;
  domain?: string;
  reward_xp: number;
  completion_mode?: string;
  completion_spec?: Record<string, unknown>;
  assignee_type?: string;
  assignee_id?: string;
  created_by: string;
  created_at: string;
}

export interface XPStatus {
  workspace_id: string;
  user_id: string;
  xp: number;
  level: number;
  joined_at: string;
}

export interface XPLevel {
  level: number;
  xp_required: number;
  title: string;
}
