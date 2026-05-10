import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import {
  Card,
  Table,
  Button,
  Space,
  Modal,
  Form,
  Input,
  Select,
  InputNumber,
  Popconfirm,
  Alert,
  Typography,
  Tabs,
  message,
} from "antd";
import {
  PlusOutlined,
  UserDeleteOutlined,
  KeyOutlined,
  DeleteOutlined,
  FileTextOutlined,
} from "@ant-design/icons";
import { projectService, apiKeyService, templateService } from "@/services/api";
import { useWorkspace } from "@/contexts/WorkspaceContext";
import type { ProjectMember, ProjectRole, APIKey, Template } from "@/config";
import {
  PROJECT_ROLES,
  TICKET_TYPES,
  PRIORITIES,
  DOMAINS,
  COMPLETION_MODES,
} from "@/config";

// ─── Members Tab ─────────────────────────────────────────────────────────────

const MembersTab: React.FC<{ wid: string; pid: string }> = ({ wid, pid }) => {
  const [members, setMembers] = useState<ProjectMember[]>([]);
  const [addOpen, setAddOpen] = useState(false);
  const [form] = Form.useForm();

  const load = async () => {
    if (!wid) return;
    try {
      const { data } = await projectService.listMembers(wid, pid);
      setMembers(data.members ?? []);
    } catch { /* permission */ }
  };

  useEffect(() => { load(); }, [wid, pid]);

  const handleAdd = async () => {
    const { user_id, role } = await form.validateFields();
    await projectService.addMember(wid, pid, user_id, role);
    message.success("Member added");
    setAddOpen(false);
    form.resetFields();
    await load();
  };

  const handleRemove = async (userId: string) => {
    await projectService.removeMember(wid, pid, userId);
    await load();
  };

  const handleSetRole = async (userId: string, role: ProjectRole) => {
    await projectService.setMemberRole(wid, pid, userId, role);
    await load();
  };

  return (
    <>
      <Table
        dataSource={members}
        rowKey="user_id"
        size="small"
        pagination={false}
        style={{ marginBottom: 16 }}
      >
        <Table.Column dataIndex="user_id" title="User ID" />
        <Table.Column
          dataIndex="role"
          title="Role"
          width={130}
          render={(role, record: ProjectMember) => (
            <Select
              value={role}
              size="small"
              style={{ width: 120 }}
              options={PROJECT_ROLES.map((r) => ({ label: r, value: r }))}
              onChange={(v) => handleSetRole(record.user_id, v)}
            />
          )}
        />
        <Table.Column
          title=""
          width={50}
          render={(_, record: ProjectMember) => (
            <Popconfirm title="Remove member?" onConfirm={() => handleRemove(record.user_id)}>
              <Button size="small" danger icon={<UserDeleteOutlined />} />
            </Popconfirm>
          )}
        />
      </Table>

      <Space>
        <Button icon={<PlusOutlined />} onClick={() => setAddOpen(true)}>
          Add Member
        </Button>
      </Space>

      <Modal
        title="Add Member"
        open={addOpen}
        onOk={handleAdd}
        onCancel={() => { setAddOpen(false); form.resetFields(); }}
        okText="Add"
      >
        <Form form={form} layout="vertical">
          <Form.Item name="user_id" label="User ID" rules={[{ required: true }]}>
            <Input placeholder="user@example.com or UUID" />
          </Form.Item>
          <Form.Item name="role" label="Role" initialValue="member">
            <Select options={PROJECT_ROLES.map((r) => ({ label: r, value: r }))} />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
};

// ─── Templates Tab ────────────────────────────────────────────────────────────

const TemplatesTab: React.FC<{ wid: string; pid: string }> = ({ wid, pid }) => {
  const [templates, setTemplates] = useState<Template[]>([]);
  const [createOpen, setCreateOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [form] = Form.useForm();

  const load = async () => {
    if (!wid || !pid) return;
    try {
      const { data } = await templateService.list(wid, pid);
      setTemplates(data.templates ?? []);
    } catch { /* permission */ }
  };

  useEffect(() => { load(); }, [wid, pid]);

  const handleCreate = async () => {
    const values = await form.validateFields();
    setSaving(true);
    try {
      await templateService.create(wid, pid, values);
      message.success("Template created");
      setCreateOpen(false);
      form.resetFields();
      await load();
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (tid: string) => {
    await templateService.delete(wid, pid, tid);
    message.success("Template deleted");
    await load();
  };

  return (
    <>
      <div style={{ marginBottom: 12, display: "flex", justifyContent: "flex-end" }}>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
          New Template
        </Button>
      </div>

      <Table
        dataSource={templates}
        rowKey="id"
        size="small"
        pagination={false}
      >
        <Table.Column dataIndex="name" title="Name" />
        <Table.Column dataIndex="title" title="Title" render={(v) => v ?? "—"} />
        <Table.Column
          dataIndex="reward_xp"
          title="XP"
          width={70}
          render={(v) => v ?? 0}
        />
        <Table.Column
          dataIndex="created_at"
          title="Created"
          width={160}
          render={(v) => v ? new Date(v).toLocaleDateString() : "—"}
        />
        <Table.Column
          title=""
          width={50}
          render={(_, record: Template) => (
            <Popconfirm title="Delete template?" onConfirm={() => handleDelete(record.id)}>
              <Button size="small" danger icon={<DeleteOutlined />} />
            </Popconfirm>
          )}
        />
      </Table>

      <Modal
        title="New Template"
        open={createOpen}
        onOk={handleCreate}
        onCancel={() => { setCreateOpen(false); form.resetFields(); }}
        okText="Create"
        confirmLoading={saving}
        width={560}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="Name" rules={[{ required: true }]}>
            <Input placeholder="e.g. bug-report" />
          </Form.Item>
          <Form.Item name="title" label="Title">
            <Input placeholder="Default ticket title (optional)" />
          </Form.Item>
          <Form.Item name="type" label="Type">
            <Select
              allowClear
              placeholder="Select type"
              options={TICKET_TYPES.map((t) => ({ label: t, value: t }))}
            />
          </Form.Item>
          <Form.Item name="priority" label="Priority">
            <Select
              allowClear
              placeholder="Select priority"
              options={PRIORITIES.map((p) => ({ label: p, value: p }))}
            />
          </Form.Item>
          <Form.Item name="reward_xp" label="Reward XP" initialValue={0}>
            <InputNumber min={0} style={{ width: "100%" }} />
          </Form.Item>
          <Form.Item name="domain" label="Domain">
            <Select
              allowClear
              placeholder="Select domain"
              options={DOMAINS.map((d) => ({ label: d, value: d }))}
            />
          </Form.Item>
          <Form.Item name="completion_mode" label="Completion Mode">
            <Select
              allowClear
              placeholder="Select completion mode"
              options={COMPLETION_MODES.map((m) => ({ label: m, value: m }))}
            />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
};

// ─── API Keys Tab ─────────────────────────────────────────────────────────────

const APIKeysTab: React.FC<{ wid: string; pid: string }> = ({ wid, pid }) => {
  const [keys, setKeys] = useState<APIKey[]>([]);
  const [createOpen, setCreateOpen] = useState(false);
  const [newKey, setNewKey] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [form] = Form.useForm();

  const load = async () => {
    if (!wid || !pid) return;
    try {
      const { data } = await apiKeyService.list(wid, pid);
      setKeys(data.api_keys ?? []);
    } catch { /* permission */ }
  };

  useEffect(() => { load(); }, [wid, pid]);

  const handleCreate = async () => {
    const { name } = await form.validateFields();
    setSaving(true);
    try {
      const { data } = await apiKeyService.create(wid, pid, name);
      setNewKey(data.key);
      form.resetFields();
      await load();
    } finally {
      setSaving(false);
    }
  };

  const handleModalClose = () => {
    setCreateOpen(false);
    setNewKey(null);
    form.resetFields();
  };

  const handleDelete = async (kid: string) => {
    await apiKeyService.delete(wid, pid, kid);
    message.success("API key deleted");
    await load();
  };

  return (
    <>
      <div style={{ marginBottom: 12, display: "flex", justifyContent: "flex-end" }}>
        <Button type="primary" icon={<KeyOutlined />} onClick={() => setCreateOpen(true)}>
          Generate Key
        </Button>
      </div>

      <Table dataSource={keys} rowKey="id" size="small" pagination={false}>
        <Table.Column dataIndex="name" title="Name" />
        <Table.Column
          dataIndex="created_at"
          title="Created"
          width={160}
          render={(v) => v ? new Date(v).toLocaleDateString() : "—"}
        />
        <Table.Column
          dataIndex="last_used_at"
          title="Last Used"
          width={160}
          render={(v) => v ? new Date(v).toLocaleDateString() : "Never"}
        />
        <Table.Column
          title=""
          width={50}
          render={(_, record: APIKey) => (
            <Popconfirm title="Delete API key?" onConfirm={() => handleDelete(record.id)}>
              <Button size="small" danger icon={<DeleteOutlined />} />
            </Popconfirm>
          )}
        />
      </Table>

      <Modal
        title="Generate API Key"
        open={createOpen}
        onOk={newKey ? handleModalClose : handleCreate}
        onCancel={handleModalClose}
        okText={newKey ? "Done" : "Generate"}
        confirmLoading={saving}
        cancelButtonProps={newKey ? { style: { display: "none" } } : {}}
      >
        {newKey ? (
          <Space direction="vertical" style={{ width: "100%" }}>
            <Alert
              type="warning"
              message="This key is shown only once. Copy it now."
              showIcon
            />
            <Typography.Text copyable code style={{ wordBreak: "break-all" }}>
              {newKey}
            </Typography.Text>
          </Space>
        ) : (
          <Form form={form} layout="vertical">
            <Form.Item name="name" label="Key Name" rules={[{ required: true }]}>
              <Input placeholder="e.g. ci-pipeline" />
            </Form.Item>
          </Form>
        )}
      </Modal>
    </>
  );
};

// ─── ProjectShow ──────────────────────────────────────────────────────────────

export const ProjectShow = () => {
  const { id: projectId } = useParams<{ id: string }>();
  const { currentWorkspace } = useWorkspace();
  const wid = currentWorkspace?.id ?? "";
  const pid = projectId!;

  const tabItems = [
    {
      key: "members",
      label: (
        <Space>
          <UserDeleteOutlined />
          Members
        </Space>
      ),
      children: <MembersTab wid={wid} pid={pid} />,
    },
    {
      key: "templates",
      label: (
        <Space>
          <FileTextOutlined />
          Templates
        </Space>
      ),
      children: <TemplatesTab wid={wid} pid={pid} />,
    },
    {
      key: "api-keys",
      label: (
        <Space>
          <KeyOutlined />
          API Keys
        </Space>
      ),
      children: <APIKeysTab wid={wid} pid={pid} />,
    },
  ];

  return (
    <Card style={{ maxWidth: 800, margin: "0 auto" }}>
      <Tabs defaultActiveKey="members" items={tabItems} />
    </Card>
  );
};
