import { Tag } from "antd";
import type { Priority } from "@/config";

const PRIORITY_COLOR: Record<Priority, string> = {
  urgent: "red",
  high: "orange",
  normal: "blue",
  low: "default",
};

export const PriorityTag = ({ priority }: { priority: Priority }) => (
  <Tag color={PRIORITY_COLOR[priority] ?? "default"}>
    {priority.charAt(0).toUpperCase() + priority.slice(1)}
  </Tag>
);
