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
  Popconfirm,
  message,
} from "antd";
import { PlusOutlined, UserDeleteOutlined } from "@ant-design/icons";
import { projectService } from "@/services/api";
import { useWorkspace } from "@/contexts/WorkspaceContext";
import type { ProjectMember, ProjectRole } from "@/config";
import { PROJECT_ROLES } from "@/config";

export const ProjectShow = () => {
  const { id: projectId } = useParams<{ id: string }>();
  const { currentWorkspace } = useWorkspace();
  const wid = currentWorkspace?.id ?? "";
  const pid = projectId!;

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

  useEffect(() => {
    load();
  }, [wid, pid]);

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
    <Card title="Project Members" style={{ maxWidth: 700, margin: "0 auto" }}>
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
    </Card>
  );
};
