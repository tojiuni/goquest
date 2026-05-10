import { useEffect, useState } from "react";
import { Create, useForm } from "@refinedev/antd";
import { Form, Input, Select, Row, Col, InputNumber, DatePicker } from "antd";
import { TICKET_TYPES, PRIORITIES, DOMAINS, COMPLETION_MODES } from "@/config";
import { useWorkspace } from "@/contexts/WorkspaceContext";
import { projectService, tagService } from "@/services/api";
import type { Project, Tag } from "@/config";

export const TicketCreate = () => {
  const { currentWorkspace } = useWorkspace();
  const wid = currentWorkspace?.id ?? "";

  const [projects, setProjects] = useState<Project[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);

  const { formProps, saveButtonProps, form } = useForm({
    resource: "tickets",
    action: "create",
    redirect: "show",
  });

  const assigneeType = Form.useWatch("assignee_type", form);
  const completionMode = Form.useWatch("completion_mode", form);

  useEffect(() => {
    if (!wid) return;
    projectService.listProjects(wid).then(({ data }) => setProjects(data.projects ?? []));
    tagService.listTags(wid).then(({ data }) => setTags(data.tags ?? []));
  }, [wid]);

  return (
    <Create saveButtonProps={saveButtonProps} title="New Ticket">
      <Form
        {...formProps}
        layout="vertical"
        initialValues={{
          type: "task",
          priority: "normal",
          assignee_type: "human",
          completion_mode: "manual",
          reward_xp: 0,
        }}
      >
        <Form.Item name="title" label="Title" rules={[{ required: true, message: "Title is required" }]}>
          <Input placeholder="Describe the task in one line" />
        </Form.Item>

        <Form.Item name="description" label="Description">
          <Input.TextArea rows={3} placeholder="Optional details…" />
        </Form.Item>

        <Row gutter={16}>
          <Col xs={24} sm={8}>
            <Form.Item name="type" label="Type" rules={[{ required: true }]}>
              <Select options={TICKET_TYPES.map((t) => ({ label: t, value: t }))} />
            </Form.Item>
          </Col>
          <Col xs={24} sm={8}>
            <Form.Item name="priority" label="Priority" rules={[{ required: true }]}>
              <Select options={PRIORITIES.map((p) => ({ label: p.charAt(0).toUpperCase() + p.slice(1), value: p }))} />
            </Form.Item>
          </Col>
          <Col xs={24} sm={8}>
            <Form.Item name="assignee_type" label="Assignee Type">
              <Select
                options={[
                  { label: "Human", value: "human" },
                  { label: "AI Agent", value: "agent" },
                ]}
                onChange={() => form.setFieldsValue({ assignee_id: undefined, domain: undefined })}
              />
            </Form.Item>
          </Col>
        </Row>

        {assigneeType === "agent" && (
          <Row gutter={16}>
            <Col xs={24} sm={12}>
              <Form.Item name="domain" label="Domain" rules={[{ required: true, message: "Domain is required for agent tickets" }]}>
                <Select
                  placeholder="Select agent domain"
                  options={DOMAINS.map((d) => ({ label: `hermes-${d}`, value: d }))}
                />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item name="assignee_id" label="Assignee ID">
                <Input placeholder="e.g. hermes-infra" />
              </Form.Item>
            </Col>
          </Row>
        )}

        {assigneeType === "human" && (
          <Form.Item name="assignee_id" label="Assignee ID">
            <Input placeholder="User ID (optional)" />
          </Form.Item>
        )}

        <Divider />

        {/* Phase-2 fields */}
        <Row gutter={16}>
          <Col xs={24} sm={8}>
            <Form.Item name="completion_mode" label="Completion Mode">
              <Select options={COMPLETION_MODES.map((m) => ({ label: m, value: m }))} />
            </Form.Item>
          </Col>
          <Col xs={24} sm={8}>
            <Form.Item name="reward_xp" label="Reward XP">
              <InputNumber min={0} style={{ width: "100%" }} />
            </Form.Item>
          </Col>
          <Col xs={24} sm={8}>
            <Form.Item name="due_at" label="Due Date">
              <DatePicker showTime style={{ width: "100%" }} />
            </Form.Item>
          </Col>
        </Row>

        {completionMode === "answer" && (
          <Row gutter={16}>
            <Col xs={24} sm={12}>
              <Form.Item name={["completion_spec", "question"]} label="Question" rules={[{ required: true }]}>
                <Input placeholder="What is the capital of France?" />
              </Form.Item>
            </Col>
            <Col xs={24} sm={6}>
              <Form.Item name={["completion_spec", "answer_type"]} label="Answer Type" initialValue="exact">
                <Select
                  options={[
                    { label: "Exact match", value: "exact" },
                    { label: "Choice", value: "choice" },
                    { label: "AI Eval", value: "ai_eval" },
                  ]}
                />
              </Form.Item>
            </Col>
            <Col xs={24} sm={6}>
              <Form.Item name={["completion_spec", "max_attempts"]} label="Max Attempts" initialValue={3}>
                <InputNumber min={1} style={{ width: "100%" }} />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item name={["completion_spec", "correct_answer"]} label="Correct Answer">
                <Input placeholder="Paris" />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item name={["completion_spec", "hint"]} label="Hint (optional)">
                <Input placeholder="Think about France…" />
              </Form.Item>
            </Col>
          </Row>
        )}

        <Row gutter={16}>
          {projects.length > 0 && (
            <Col xs={24} sm={12}>
              <Form.Item name="project_id" label="Project">
                <Select
                  allowClear
                  placeholder="Select project (optional)"
                  options={projects.map((p) => ({ label: p.name, value: p.id }))}
                />
              </Form.Item>
            </Col>
          )}
          {tags.length > 0 && (
            <Col xs={24} sm={12}>
              <Form.Item name="tag_ids" label="Tags">
                <Select
                  mode="multiple"
                  allowClear
                  placeholder="Select tags"
                  options={tags.map((t) => ({ label: t.name, value: t.id }))}
                />
              </Form.Item>
            </Col>
          )}
        </Row>
      </Form>
    </Create>
  );
};

// Inline divider to avoid extra import
const Divider = () => (
  <div style={{ borderTop: "1px solid #f0f0f0", margin: "16px 0" }} />
);
