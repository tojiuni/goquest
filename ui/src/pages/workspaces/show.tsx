import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import {
  Tabs,
  Card,
  Table,
  Button,
  Space,
  Tag,
  Modal,
  Form,
  Input,
  Select,
  Popconfirm,
  ColorPicker,
  message,
} from "antd";
import { PlusOutlined, DeleteOutlined, UserDeleteOutlined } from "@ant-design/icons";
import { workspaceService, tagService } from "@/services/api";
import type { WorkspaceMember, WorkspaceInvitation, Tag as TagType, WorkspaceRole } from "@/config";
import { WORKSPACE_ROLES } from "@/config";

export const WorkspaceShow = () => {
  const { id: workspaceId } = useParams<{ id: string }>();
  const wid = workspaceId!;

  const [members, setMembers] = useState<WorkspaceMember[]>([]);
  const [invitations, setInvitations] = useState<WorkspaceInvitation[]>([]);
  const [tags, setTags] = useState<TagType[]>([]);

  const [inviteOpen, setInviteOpen] = useState(false);
  const [tagOpen, setTagOpen] = useState(false);
  const [inviteForm] = Form.useForm();
  const [tagForm] = Form.useForm();

  const loadMembers = async () => {
    try {
      const { data } = await workspaceService.listMembers(wid);
      setMembers(data.members ?? []);
    } catch { /* permission */ }
  };

  const loadInvitations = async () => {
    try {
      const { data } = await workspaceService.listInvitations(wid);
      setInvitations(data.invitations ?? []);
    } catch { /* permission */ }
  };

  const loadTags = async () => {
    try {
      const { data } = await tagService.listTags(wid);
      setTags(data.tags ?? []);
    } catch { /* permission */ }
  };

  useEffect(() => {
    loadMembers();
    loadInvitations();
    loadTags();
  }, [wid]);

  const handleInvite = async () => {
    const values = await inviteForm.validateFields();
    await workspaceService.createInvitation(wid, values);
    message.success("Invitation sent");
    setInviteOpen(false);
    inviteForm.resetFields();
    await loadInvitations();
  };

  const handleCreateTag = async () => {
    const { name, color } = await tagForm.validateFields();
    await tagService.createTag(wid, { name, color: typeof color === "string" ? color : color?.toHexString?.() ?? "#1677ff" });
    message.success("Tag created");
    setTagOpen(false);
    tagForm.resetFields();
    await loadTags();
  };

  const handleDeleteTag = async (tagId: string) => {
    await tagService.deleteTag(wid, tagId);
    setTags((prev) => prev.filter((t) => t.id !== tagId));
  };

  const handleRemoveMember = async (userId: string) => {
    await workspaceService.removeMember(wid, userId);
    await loadMembers();
  };

  const handleSetRole = async (userId: string, role: WorkspaceRole) => {
    await workspaceService.setMemberRole(wid, userId, role);
    await loadMembers();
  };

  const handleRevokeInvite = async (invId: string) => {
    await workspaceService.deleteInvitation(wid, invId);
    await loadInvitations();
  };

  return (
    <Card title="Workspace Settings" style={{ maxWidth: 900, margin: "0 auto" }}>
      <Tabs
        items={[
          {
            key: "members",
            label: "Members",
            children: (
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
                    width={120}
                    render={(role, record: WorkspaceMember) => (
                      <Select
                        value={role}
                        size="small"
                        style={{ width: 110 }}
                        options={WORKSPACE_ROLES.map((r) => ({ label: r, value: r }))}
                        onChange={(v) => handleSetRole(record.user_id, v)}
                      />
                    )}
                  />
                  <Table.Column
                    title=""
                    width={50}
                    render={(_, record: WorkspaceMember) => (
                      <Popconfirm title="Remove member?" onConfirm={() => handleRemoveMember(record.user_id)}>
                        <Button size="small" danger icon={<UserDeleteOutlined />} />
                      </Popconfirm>
                    )}
                  />
                </Table>
                <Button icon={<PlusOutlined />} onClick={() => setInviteOpen(true)}>
                  Invite Member
                </Button>
              </>
            ),
          },
          {
            key: "invitations",
            label: "Invitations",
            children: (
              <Table dataSource={invitations} rowKey="id" size="small" pagination={false}>
                <Table.Column dataIndex="invitee_email" title="Email" />
                <Table.Column
                  dataIndex="role"
                  title="Role"
                  width={100}
                  render={(v) => <Tag>{v}</Tag>}
                />
                <Table.Column
                  dataIndex="used"
                  title="Status"
                  width={90}
                  render={(v) => <Tag color={v ? "default" : "blue"}>{v ? "used" : "pending"}</Tag>}
                />
                <Table.Column
                  title=""
                  width={50}
                  render={(_, record: WorkspaceInvitation) => (
                    <Popconfirm title="Revoke invitation?" onConfirm={() => handleRevokeInvite(record.id)}>
                      <Button size="small" danger icon={<DeleteOutlined />} disabled={record.used} />
                    </Popconfirm>
                  )}
                />
              </Table>
            ),
          },
          {
            key: "tags",
            label: "Tags",
            children: (
              <>
                <Space wrap style={{ marginBottom: 16 }}>
                  {tags.map((t) => (
                    <Tag
                      key={t.id}
                      color={t.color}
                      closable
                      onClose={() => handleDeleteTag(t.id)}
                      style={{ fontSize: 13, padding: "2px 10px" }}
                    >
                      {t.name}
                    </Tag>
                  ))}
                </Space>
                <Button icon={<PlusOutlined />} onClick={() => setTagOpen(true)}>
                  New Tag
                </Button>
              </>
            ),
          },
        ]}
      />

      {/* Invite Modal */}
      <Modal
        title="Invite Member"
        open={inviteOpen}
        onOk={handleInvite}
        onCancel={() => { setInviteOpen(false); inviteForm.resetFields(); }}
        okText="Send Invitation"
      >
        <Form form={inviteForm} layout="vertical">
          <Form.Item name="invitee_email" label="Email" rules={[{ required: true, type: "email" }]}>
            <Input placeholder="user@example.com" />
          </Form.Item>
          <Form.Item name="role" label="Role" initialValue="member">
            <Select options={WORKSPACE_ROLES.map((r) => ({ label: r, value: r }))} />
          </Form.Item>
        </Form>
      </Modal>

      {/* Tag Modal */}
      <Modal
        title="New Tag"
        open={tagOpen}
        onOk={handleCreateTag}
        onCancel={() => { setTagOpen(false); tagForm.resetFields(); }}
        okText="Create"
      >
        <Form form={tagForm} layout="vertical">
          <Form.Item name="name" label="Name" rules={[{ required: true }]}>
            <Input placeholder="backend" />
          </Form.Item>
          <Form.Item name="color" label="Color" initialValue="#1677ff">
            <ColorPicker />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  );
};

export default WorkspaceShow;
