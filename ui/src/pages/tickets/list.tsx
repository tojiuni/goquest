import { useEffect, useState, useCallback } from "react";
import { List, useTable, DateField } from "@refinedev/antd";
import { useNavigation } from "@refinedev/core";
import {
  Table,
  Space,
  Select,
  Button,
  Typography,
  Tag,
  Badge,
  message,
} from "antd";
import { useSSE } from "@/hooks/useSSE";
import { PlusOutlined, EyeOutlined, EditOutlined, TrophyOutlined } from "@ant-design/icons";
import { StatusTag } from "@/components/StatusTag";
import { PriorityTag } from "@/components/PriorityTag";
import { TICKET_STATUSES, TICKET_TYPES, PRIORITIES, DOMAINS } from "@/config";
import type { TicketStatus, Priority, Ticket, Project } from "@/config";
import { useWorkspace } from "@/contexts/WorkspaceContext";
import { projectService } from "@/services/api";

export const TicketList = () => {
  const { show, edit, create } = useNavigation();
  const { currentWorkspace } = useWorkspace();
  const [projects, setProjects] = useState<Project[]>([]);

  const { tableProps, setFilters, tableQueryResult } = useTable<Ticket>({
    resource: "tickets",
    syncWithLocation: true,
    pagination: { pageSize: 20 },
  });

  const handleSSEEvent = useCallback(
    (event: { stream: string; data: unknown }) => {
      if (event.stream === "goquest.tickets.created") {
        message.info("New ticket created");
        tableQueryResult.refetch();
      } else if (event.stream === "goquest.tickets.completed") {
        message.success("Ticket completed ✓");
        tableQueryResult.refetch();
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [tableQueryResult.refetch]
  );

  useSSE(handleSSEEvent);

  useEffect(() => {
    if (!currentWorkspace?.id) return;
    projectService.listProjects(currentWorkspace.id).then(({ data }) => setProjects(data.projects ?? []));
  }, [currentWorkspace?.id]);

  return (
    <List
      headerButtons={
        <Button type="primary" icon={<PlusOutlined />} onClick={() => create("tickets")}>
          New Ticket
        </Button>
      }
    >
      <Space style={{ marginBottom: 16, flexWrap: "wrap" }} size="small">
        <Select
          placeholder="Status"
          allowClear
          style={{ width: 140 }}
          options={TICKET_STATUSES.map((s) => ({ label: s.replace("_", " "), value: s }))}
          onChange={(v) => setFilters([{ field: "status", operator: "eq", value: v }], "merge")}
        />
        <Select
          placeholder="Type"
          allowClear
          style={{ width: 120 }}
          options={TICKET_TYPES.map((t) => ({ label: t, value: t }))}
          onChange={(v) => setFilters([{ field: "type", operator: "eq", value: v }], "merge")}
        />
        <Select
          placeholder="Priority"
          allowClear
          style={{ width: 120 }}
          options={PRIORITIES.map((p) => ({ label: p, value: p }))}
          onChange={(v) => setFilters([{ field: "priority", operator: "eq", value: v }], "merge")}
        />
        <Select
          placeholder="Assignee"
          allowClear
          style={{ width: 130 }}
          options={[
            { label: "Human", value: "human" },
            { label: "Agent", value: "agent" },
          ]}
          onChange={(v) => setFilters([{ field: "assignee_type", operator: "eq", value: v }], "merge")}
        />
        <Select
          placeholder="Domain"
          allowClear
          style={{ width: 120 }}
          options={DOMAINS.map((d) => ({ label: d, value: d }))}
          onChange={(v) => setFilters([{ field: "domain", operator: "eq", value: v }], "merge")}
        />
        {projects.length > 0 && (
          <Select
            placeholder="Project"
            allowClear
            style={{ width: 150 }}
            options={projects.map((p) => ({ label: p.name, value: p.id }))}
            onChange={(v) => setFilters([{ field: "project_id", operator: "eq", value: v }], "merge")}
          />
        )}
      </Space>

      <Table
        {...tableProps}
        rowKey="id"
        size="small"
        scroll={{ x: 1000 }}
        onRow={(record) => ({ onClick: () => record.id && show("tickets", record.id) })}
        rowClassName="cursor-pointer"
      >
        <Table.Column
          dataIndex="title"
          title="Title"
          render={(v, record: Ticket) => (
            <Space direction="vertical" size={0}>
              <Typography.Text strong>{v}</Typography.Text>
              <Space size={4}>
                <Typography.Text type="secondary" style={{ fontSize: 11 }}>
                  {record.id.slice(0, 8)}
                </Typography.Text>
                {record.tags?.map((t) => (
                  <Tag key={t.id} color={t.color} style={{ fontSize: 10, padding: "0 4px", lineHeight: "16px" }}>
                    {t.name}
                  </Tag>
                ))}
              </Space>
            </Space>
          )}
        />
        <Table.Column
          dataIndex="status"
          title="Status"
          render={(v: TicketStatus) => <StatusTag status={v} />}
          width={120}
        />
        <Table.Column
          dataIndex="priority"
          title="Priority"
          render={(v) => <PriorityTag priority={v as Priority} />}
          width={100}
        />
        <Table.Column
          dataIndex="type"
          title="Type"
          width={90}
          render={(v) => <Tag>{v}</Tag>}
        />
        <Table.Column
          dataIndex="reward_xp"
          title={<TrophyOutlined style={{ color: "#faad14" }} />}
          width={60}
          render={(v) => v > 0 ? <Badge count={`+${v}`} color="#faad14" /> : null}
        />
        <Table.Column
          dataIndex="assignee_id"
          title="Assignee"
          width={140}
          render={(v, record: Ticket) =>
            v ? (
              <Space size={4}>
                <Tag color={record.assignee_type === "agent" ? "purple" : "cyan"}>
                  {record.assignee_type === "agent" ? "🤖" : "👤"}
                </Tag>
                <Typography.Text>{v}</Typography.Text>
              </Space>
            ) : (
              <Typography.Text type="secondary">—</Typography.Text>
            )
          }
        />
        <Table.Column
          dataIndex="domain"
          title="Domain"
          width={90}
          render={(v) => v ? <Tag color="geekblue">{v}</Tag> : "—"}
        />
        <Table.Column
          dataIndex="created_at"
          title="Created"
          width={120}
          render={(v) => <DateField value={v} format="MM/DD HH:mm" />}
        />
        <Table.Column
          title="Actions"
          width={90}
          render={(_, record: Ticket) => (
            <Space>
              <Button
                size="small"
                icon={<EyeOutlined />}
                onClick={(e) => { e.stopPropagation(); show("tickets", record.id); }}
              />
              <Button
                size="small"
                icon={<EditOutlined />}
                onClick={(e) => { e.stopPropagation(); edit("tickets", record.id); }}
                disabled={["completed", "cancelled"].includes(record.status)}
              />
            </Space>
          )}
        />
      </Table>
    </List>
  );
};
