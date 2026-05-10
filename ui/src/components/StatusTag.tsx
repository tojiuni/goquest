import { Tag } from "antd";
import type { TicketStatus } from "@/config";

const STATUS_COLOR: Record<TicketStatus, string> = {
  pending: "default",
  assigned: "blue",
  in_progress: "processing",
  completed: "success",
  failed: "error",
  cancelled: "warning",
};

const STATUS_LABEL: Record<TicketStatus, string> = {
  pending: "Pending",
  assigned: "Assigned",
  in_progress: "In Progress",
  completed: "Completed",
  failed: "Failed",
  cancelled: "Cancelled",
};

export const StatusTag = ({ status }: { status: TicketStatus }) => (
  <Tag color={STATUS_COLOR[status] ?? "default"}>
    {STATUS_LABEL[status] ?? status}
  </Tag>
);
