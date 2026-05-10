import { Edit, useForm } from "@refinedev/antd";
import { Form, Input, Select, Row, Col } from "antd";
import { PRIORITIES, DOMAINS } from "@/config";
import type { TicketStatus } from "@/config";

const TERMINAL: TicketStatus[] = ["completed", "cancelled"];

// Valid next statuses per current status (mirrors backend state machine)
const ALLOWED_TRANSITIONS: Record<TicketStatus, TicketStatus[]> = {
  pending: ["assigned", "cancelled"],
  assigned: ["in_progress", "pending", "cancelled"],
  in_progress: ["completed", "failed"],
  failed: ["pending"],
  completed: [],
  cancelled: [],
};

export const TicketEdit = () => {
  const { formProps, saveButtonProps, queryResult } = useForm({
    resource: "tickets",
    action: "edit",
    redirect: "show",
  });

  const current = queryResult?.data?.data as { status?: TicketStatus } | undefined;
  const currentStatus: TicketStatus = current?.status ?? "pending";
  const isTerminal = TERMINAL.includes(currentStatus);
  const allowedStatuses = isTerminal
    ? [currentStatus]
    : [currentStatus, ...ALLOWED_TRANSITIONS[currentStatus]];

  return (
    <Edit saveButtonProps={{ ...saveButtonProps, disabled: isTerminal }}>
      <Form {...formProps} layout="vertical">
        <Form.Item name="title" label="Title" rules={[{ required: true }]}>
          <Input disabled={isTerminal} />
        </Form.Item>

        <Form.Item name="description" label="Description">
          <Input.TextArea rows={3} disabled={isTerminal} />
        </Form.Item>

        <Row gutter={16}>
          <Col xs={24} sm={8}>
            <Form.Item name="status" label="Status">
              <Select
                disabled={isTerminal}
                options={allowedStatuses.map((s) => ({
                  label: s.replace("_", " "),
                  value: s,
                }))}
              />
            </Form.Item>
          </Col>
          <Col xs={24} sm={8}>
            <Form.Item name="priority" label="Priority">
              <Select
                disabled={isTerminal}
                options={PRIORITIES.map((p) => ({
                  label: p.charAt(0).toUpperCase() + p.slice(1),
                  value: p,
                }))}
              />
            </Form.Item>
          </Col>
          <Col xs={24} sm={8}>
            <Form.Item name="assignee_type" label="Assignee Type">
              <Select
                disabled={isTerminal}
                options={[
                  { label: "Human", value: "human" },
                  { label: "AI Agent", value: "agent" },
                ]}
              />
            </Form.Item>
          </Col>
        </Row>

        <Row gutter={16}>
          <Col xs={24} sm={12}>
            <Form.Item name="assignee_id" label="Assignee ID">
              <Input disabled={isTerminal} />
            </Form.Item>
          </Col>
          <Col xs={24} sm={12}>
            <Form.Item name="domain" label="Domain">
              <Select
                disabled={isTerminal}
                allowClear
                options={DOMAINS.map((d) => ({ label: d, value: d }))}
              />
            </Form.Item>
          </Col>
        </Row>
      </Form>
    </Edit>
  );
};
