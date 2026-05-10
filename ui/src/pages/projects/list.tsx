import { useState } from "react";
import { useNavigation } from "@refinedev/core";
import {
  List,
  DateField,
} from "@refinedev/antd";
import { Table, Button, Space, Tag, Modal, Form, Input, Typography } from "antd";
import { PlusOutlined, FolderOpenOutlined } from "@ant-design/icons";
import { useWorkspace } from "@/contexts/WorkspaceContext";
import { projectService } from "@/services/api";
import type { Project } from "@/config";
import { useTable } from "@refinedev/antd";

export const ProjectList = () => {
  const { show } = useNavigation();
  const { currentWorkspace } = useWorkspace();
  const [creating, setCreating] = useState(false);
  const [form] = Form.useForm();

  const { tableProps, tableQueryResult } = useTable<Project>({
    resource: "projects",
    meta: { workspaceId: currentWorkspace?.id },
    pagination: { pageSize: 50 },
    queryOptions: { enabled: !!currentWorkspace },
  });

  const handleCreate = async () => {
    if (!currentWorkspace) return;
    const values = await form.validateFields();
    await projectService.createProject(currentWorkspace.id, values);
    setCreating(false);
    form.resetFields();
    tableQueryResult.refetch();
  };

  return (
    <List
      title={currentWorkspace ? `Projects — ${currentWorkspace.name}` : "Projects"}
      headerButtons={
        <Button
          type="primary"
          icon={<PlusOutlined />}
          onClick={() => setCreating(true)}
          disabled={!currentWorkspace}
        >
          New Project
        </Button>
      }
    >
      <Table
        {...tableProps}
        rowKey="id"
        size="small"
        onRow={(record) => ({
          onClick: () => show("projects", record.id),
          style: { cursor: "pointer" },
        })}
      >
        <Table.Column
          dataIndex="name"
          title="Name"
          render={(v, record: Project) => (
            <Space>
              <FolderOpenOutlined style={{ color: "#1677ff" }} />
              <Space direction="vertical" size={0}>
                <Typography.Text strong>{v}</Typography.Text>
                {record.description && (
                  <Typography.Text type="secondary" style={{ fontSize: 11 }}>
                    {record.description}
                  </Typography.Text>
                )}
              </Space>
            </Space>
          )}
        />
        <Table.Column
          dataIndex="status"
          title="Status"
          width={90}
          render={(v) => <Tag color={v === "active" ? "green" : "default"}>{v}</Tag>}
        />
        <Table.Column
          dataIndex="created_at"
          title="Created"
          width={130}
          render={(v) => <DateField value={v} format="YYYY-MM-DD" />}
        />
      </Table>

      <Modal
        title="New Project"
        open={creating}
        onOk={handleCreate}
        onCancel={() => { setCreating(false); form.resetFields(); }}
        okText="Create"
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="Project Name" rules={[{ required: true }]}>
            <Input placeholder="My Project" />
          </Form.Item>
          <Form.Item name="description" label="Description">
            <Input.TextArea rows={2} placeholder="Optional description" />
          </Form.Item>
        </Form>
      </Modal>
    </List>
  );
};
