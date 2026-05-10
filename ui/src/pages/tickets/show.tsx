import { useEffect, useState } from "react";
import { Show, DateField } from "@refinedev/antd";
import { useShow, useUpdate } from "@refinedev/core";
import {
  Descriptions,
  Tag,
  Space,
  Button,
  Popconfirm,
  Typography,
  Divider,
  Alert,
  Table,
  Input,
  Form,
  Badge,
  message,
} from "antd";
import {
  PlayCircleOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
  RedoOutlined,
  TrophyOutlined,
} from "@ant-design/icons";
import { StatusTag } from "@/components/StatusTag";
import { PriorityTag } from "@/components/PriorityTag";
import { ticketService } from "@/services/api";
import type { TicketStatus, Ticket, Attempt } from "@/config";

const TRANSITIONS: Record<TicketStatus, TicketStatus[]> = {
  pending: ["assigned", "cancelled"],
  assigned: ["in_progress", "pending", "cancelled"],
  in_progress: ["completed", "failed"],
  failed: ["pending"],
  completed: [],
  cancelled: [],
};

const TRANSITION_BUTTONS: Partial<Record<TicketStatus, { label: string; icon: React.ReactNode; danger?: boolean }>> = {
  in_progress: { label: "Start", icon: <PlayCircleOutlined /> },
  completed: { label: "Complete", icon: <CheckCircleOutlined /> },
  failed: { label: "Mark Failed", icon: <CloseCircleOutlined />, danger: true },
  cancelled: { label: "Cancel", icon: <CloseCircleOutlined />, danger: true },
  pending: { label: "Retry", icon: <RedoOutlined /> },
  assigned: { label: "Re-assign", icon: <RedoOutlined /> },
};

export const TicketShow = () => {
  const { queryResult } = useShow<Ticket>({ resource: "tickets" });
  const { mutate: update, isLoading: isUpdating } = useUpdate();
  const ticket = queryResult?.data?.data;

  const [subtasks, setSubtasks] = useState<Ticket[]>([]);
  const [attempts, setAttempts] = useState<Attempt[]>([]);
  const [answerForm] = Form.useForm();
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!ticket?.id) return;
    ticketService.listSubtasks(ticket.id).then(({ data }) => setSubtasks(data.subtasks ?? []));
    if (ticket.completion_mode === "answer") {
      ticketService.listAttempts(ticket.id).then(({ data }) => setAttempts(data.attempts ?? []));
    }
  }, [ticket?.id, ticket?.completion_mode]);

  const transition = (toStatus: TicketStatus) => {
    if (!ticket) return;
    update({ resource: "tickets", id: ticket.id, values: { status: toStatus } });
  };

  const handleSubmitAnswer = async () => {
    if (!ticket) return;
    const { answer } = await answerForm.validateFields();
    setSubmitting(true);
    try {
      const { data } = await ticketService.submitAttempt(ticket.id, answer);
      setAttempts((prev) => [...prev, data]);
      answerForm.resetFields();
      if (data.is_correct) {
        message.success("Correct! Ticket completed.");
        queryResult.refetch();
      } else {
        message.warning("Wrong answer. Try again.");
      }
    } catch (err: unknown) {
      const msg = (err as { response?: { data?: { error?: string } } })?.response?.data?.error ?? "Submission failed";
      message.error(msg);
    } finally {
      setSubmitting(false);
    }
  };

  const nextStatuses = ticket ? TRANSITIONS[ticket.status] : [];
  const isTerminal = ticket && ["completed", "cancelled", "failed"].includes(ticket.status);

  return (
    <Show title={ticket?.title ?? "Ticket"} canEdit={!isTerminal}>
      {isTerminal && (
        <Alert
          type={ticket?.status === "completed" ? "success" : "warning"}
          message={`This ticket is ${ticket?.status} and cannot be modified.`}
          style={{ marginBottom: 16 }}
        />
      )}

      {nextStatuses.length > 0 && (
        <Space style={{ marginBottom: 16 }}>
          {nextStatuses.map((s) => {
            const btn = TRANSITION_BUTTONS[s];
            if (!btn) return null;
            return (
              <Popconfirm key={s} title={`Move to "${s.replace("_", " ")}"?`} onConfirm={() => transition(s)}>
                <Button
                  type={s === "completed" ? "primary" : "default"}
                  danger={btn.danger}
                  icon={btn.icon}
                  loading={isUpdating}
                >
                  {btn.label}
                </Button>
              </Popconfirm>
            );
          })}
        </Space>
      )}

      <Divider />

      <Descriptions bordered column={2} size="small">
        <Descriptions.Item label="ID" span={2}>
          <Typography.Text code>{ticket?.id}</Typography.Text>
        </Descriptions.Item>
        <Descriptions.Item label="Status">
          {ticket && <StatusTag status={ticket.status} />}
        </Descriptions.Item>
        <Descriptions.Item label="Priority">
          {ticket && <PriorityTag priority={ticket.priority as never} />}
        </Descriptions.Item>
        <Descriptions.Item label="Type">
          <Tag>{ticket?.type}</Tag>
        </Descriptions.Item>
        <Descriptions.Item label="Domain">
          {ticket?.domain ? <Tag color="geekblue">{ticket.domain}</Tag> : "—"}
        </Descriptions.Item>
        <Descriptions.Item label="Completion Mode">
          <Tag color="purple">{ticket?.completion_mode ?? "manual"}</Tag>
        </Descriptions.Item>
        <Descriptions.Item label="Visibility">
          <Tag>{ticket?.visibility ?? "project"}</Tag>
        </Descriptions.Item>
        {(ticket?.reward_xp ?? 0) > 0 && (
          <Descriptions.Item label="Reward XP">
            <Space>
              <TrophyOutlined style={{ color: "#faad14" }} />
              <Typography.Text strong style={{ color: "#faad14" }}>
                +{ticket?.reward_xp} XP
              </Typography.Text>
            </Space>
          </Descriptions.Item>
        )}
        {ticket?.due_at && (
          <Descriptions.Item label="Due Date">
            <DateField value={ticket.due_at} format="YYYY-MM-DD HH:mm" />
          </Descriptions.Item>
        )}
        <Descriptions.Item label="Assignee">
          {ticket?.assignee_id ? (
            <Space>
              <Tag color={ticket.assignee_type === "agent" ? "purple" : "cyan"}>{ticket.assignee_type}</Tag>
              {ticket.assignee_id}
            </Space>
          ) : "—"}
        </Descriptions.Item>
        <Descriptions.Item label="Requester">{ticket?.requester_id ?? "—"}</Descriptions.Item>
        {ticket?.project_id && (
          <Descriptions.Item label="Project ID">
            <Typography.Text code>{ticket.project_id}</Typography.Text>
          </Descriptions.Item>
        )}
        {ticket?.parent_id && (
          <Descriptions.Item label="Parent Ticket">
            <Typography.Text code>{ticket.parent_id}</Typography.Text>
          </Descriptions.Item>
        )}
        <Descriptions.Item label="Description" span={2}>
          {ticket?.description || <Typography.Text type="secondary">No description</Typography.Text>}
        </Descriptions.Item>
        {ticket?.tags && ticket.tags.length > 0 && (
          <Descriptions.Item label="Tags" span={2}>
            <Space wrap>
              {ticket.tags.map((t) => (
                <Tag key={t.id} color={t.color}>{t.name}</Tag>
              ))}
            </Space>
          </Descriptions.Item>
        )}
        {ticket?.agent_context && (
          <Descriptions.Item label="Agent Context" span={2}>
            <Typography.Text code style={{ whiteSpace: "pre-wrap", fontSize: 12 }}>
              {JSON.stringify(ticket.agent_context, null, 2)}
            </Typography.Text>
          </Descriptions.Item>
        )}
        <Descriptions.Item label="Created">
          {ticket && <DateField value={ticket.created_at} format="YYYY-MM-DD HH:mm:ss" />}
        </Descriptions.Item>
        <Descriptions.Item label="Updated">
          {ticket && <DateField value={ticket.updated_at} format="YYYY-MM-DD HH:mm:ss" />}
        </Descriptions.Item>
        {ticket?.completed_at && (
          <Descriptions.Item label="Completed">
            <DateField value={ticket.completed_at} format="YYYY-MM-DD HH:mm:ss" />
          </Descriptions.Item>
        )}
      </Descriptions>

      {/* Subtasks */}
      {subtasks.length > 0 && (
        <>
          <Divider orientation="left">Subtasks</Divider>
          <Table
            dataSource={subtasks}
            rowKey="id"
            size="small"
            pagination={false}
          >
            <Table.Column dataIndex="title" title="Title" />
            <Table.Column
              dataIndex="status"
              title="Status"
              width={110}
              render={(v) => <StatusTag status={v} />}
            />
            <Table.Column
              dataIndex="priority"
              title="Priority"
              width={90}
              render={(v) => <PriorityTag priority={v} />}
            />
            <Table.Column
              dataIndex="reward_xp"
              title="XP"
              width={70}
              render={(v) => v > 0 ? <Badge count={`+${v}`} color="#faad14" /> : null}
            />
          </Table>
        </>
      )}

      {/* Answer Mode */}
      {ticket?.completion_mode === "answer" && !isTerminal && (
        <>
          <Divider orientation="left">Submit Answer</Divider>
          {ticket.completion_spec?.question && (
            <Typography.Paragraph strong style={{ marginBottom: 12 }}>
              {String(ticket.completion_spec.question)}
            </Typography.Paragraph>
          )}
          {ticket.completion_spec?.hint && (
            <Alert
              type="info"
              message={`Hint: ${ticket.completion_spec.hint}`}
              style={{ marginBottom: 12 }}
            />
          )}
          <Form form={answerForm} layout="inline" onFinish={handleSubmitAnswer}>
            <Form.Item name="answer" rules={[{ required: true }]} style={{ flex: 1 }}>
              <Input placeholder="Your answer…" />
            </Form.Item>
            <Form.Item>
              <Button type="primary" htmlType="submit" loading={submitting}>
                Submit
              </Button>
            </Form.Item>
          </Form>
        </>
      )}

      {/* Attempts History */}
      {attempts.length > 0 && (
        <>
          <Divider orientation="left">Attempts</Divider>
          <Table dataSource={attempts} rowKey="id" size="small" pagination={false}>
            <Table.Column dataIndex="answer" title="Answer" />
            <Table.Column
              dataIndex="is_correct"
              title="Result"
              width={90}
              render={(v) =>
                v === null || v === undefined ? (
                  <Tag>pending</Tag>
                ) : (
                  <Tag color={v ? "success" : "error"}>{v ? "Correct" : "Wrong"}</Tag>
                )
              }
            />
            <Table.Column dataIndex="ai_feedback" title="Feedback" render={(v) => v || "—"} />
            <Table.Column
              dataIndex="attempted_at"
              title="Time"
              width={140}
              render={(v) => <DateField value={v} format="MM-DD HH:mm:ss" />}
            />
          </Table>
        </>
      )}
    </Show>
  );
};
