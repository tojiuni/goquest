import { useState } from "react";
import { List, DateField, useTable } from "@refinedev/antd";
import { useNavigation } from "@refinedev/core";
import {
  Table,
  Button,
  Space,
  Tag,
  Modal,
  Form,
  Input,
  Select,
  Typography,
} from "antd";
import { PlusOutlined, SettingOutlined } from "@ant-design/icons";
import { useWorkspace } from "@/contexts/WorkspaceContext";
import { workspaceService } from "@/services/api";
import type { Workspace } from "@/config";

export const WorkspaceList = () => {
  const { show } = useNavigation();
  const { setCurrentWorkspace, refresh } = useWorkspace();
  const [creating, setCreating] = useState(false);
  const [form] = Form.useForm();

  const { tableProps } = useTable<Workspace>({
    resource: "workspaces",
    pagination: { pageSize: 50 },
  });

  const handleCreate = async () => {
    const values = await form.validateFields();
    await workspaceService.createWorkspace(values);
    await refresh();
    setCreating(false);
    form.resetFields();
  };

  return (
    <List
      headerButtons={
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreating(true)}>
          New Workspace
        </Button>
      }
    >
      <Table
        {...tableProps}
        rowKey="id"
        size="small"
        onRow={(record) => ({
          onClick: () => {
            setCurrentWorkspace(record);
            show("workspaces", record.id);
          },
          style: { cursor: "pointer" },
        })}
      >
        <Table.Column
          dataIndex="name"
          title="Name"
          render={(v, record: Workspace) => (
            <Space direction="vertical" size={0}>
              <Typography.Text strong>{v}</Typography.Text>
              <Typography.Text type="secondary" style={{ fontSize: 11 }}>
                {record.slug}
              </Typography.Text>
            </Space>
          )}
        />
        <Table.Column
          dataIndex="type"
          title="Type"
          width={100}
          render={(v) => <Tag color={v === "personal" ? "blue" : "green"}>{v}</Tag>}
        />
        <Table.Column
          dataIndex="created_at"
          title="Created"
          width={130}
          render={(v) => <DateField value={v} format="YYYY-MM-DD" />}
        />
        <Table.Column
          title=""
          width={60}
          render={(_, record: Workspace) => (
            <Button
              size="small"
              icon={<SettingOutlined />}
              onClick={(e) => {
                e.stopPropagation();
                setCurrentWorkspace(record);
                show("workspaces", record.id);
              }}
            />
          )}
        />
      </Table>

      <Modal
        title="New Workspace"
        open={creating}
        onOk={handleCreate}
        onCancel={() => { setCreating(false); form.resetFields(); }}
        okText="Create"
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="Name" rules={[{ required: true }]}>
            <Input placeholder="My Team Workspace" />
          </Form.Item>
          <Form.Item name="type" label="Type" initialValue="shared">
            <Select
              options={[
                { label: "Shared (team)", value: "shared" },
                { label: "Personal", value: "personal" },
              ]}
            />
          </Form.Item>
        </Form>
      </Modal>
    </List>
  );
};
