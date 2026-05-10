import { describe, it, expect } from "vitest";
import {
  TICKET_STATUSES,
  TICKET_TYPES,
  PRIORITIES,
  DOMAINS,
  COMPLETION_MODES,
  VISIBILITIES,
  WORKSPACE_ROLES,
  PROJECT_ROLES,
} from "@/config";

describe("config constants", () => {
  it("ticket statuses include all six states", () => {
    expect(TICKET_STATUSES).toContain("pending");
    expect(TICKET_STATUSES).toContain("assigned");
    expect(TICKET_STATUSES).toContain("in_progress");
    expect(TICKET_STATUSES).toContain("completed");
    expect(TICKET_STATUSES).toContain("failed");
    expect(TICKET_STATUSES).toContain("cancelled");
  });

  it("ticket types cover all five variants", () => {
    expect(TICKET_TYPES).toHaveLength(5);
    expect(TICKET_TYPES).toContain("task");
    expect(TICKET_TYPES).toContain("bug");
    expect(TICKET_TYPES).toContain("feature");
  });

  it("priorities have four levels in descending order", () => {
    expect(PRIORITIES).toEqual(["urgent", "high", "normal", "low"]);
  });

  it("domains map to hermes agent types", () => {
    const expected = ["infra", "data", "code", "monitor", "knowledge", "pm"];
    expected.forEach((d) => expect(DOMAINS).toContain(d));
  });

  it("completion modes include all four Phase-2 modes", () => {
    expect(COMPLETION_MODES).toContain("manual");
    expect(COMPLETION_MODES).toContain("answer");
    expect(COMPLETION_MODES).toContain("agent");
    expect(COMPLETION_MODES).toContain("subtask");
  });

  it("visibility has project and restricted", () => {
    expect(VISIBILITIES).toContain("project");
    expect(VISIBILITIES).toContain("restricted");
  });

  it("workspace roles ordered owner > admin > member > viewer", () => {
    expect(WORKSPACE_ROLES).toEqual(["owner", "admin", "member", "viewer"]);
  });

  it("project roles are admin, member, viewer", () => {
    expect(PROJECT_ROLES).toEqual(["admin", "member", "viewer"]);
  });
});
