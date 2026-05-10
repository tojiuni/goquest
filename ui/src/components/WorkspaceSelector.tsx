import { Select, Spin, Typography } from "antd";
import { TeamOutlined } from "@ant-design/icons";
import { useWorkspace } from "@/contexts/WorkspaceContext";

export const WorkspaceSelector: React.FC = () => {
  const { workspaces, currentWorkspace, loading, setCurrentWorkspace } = useWorkspace();

  if (loading) return <Spin size="small" />;
  if (workspaces.length === 0) return <Typography.Text type="secondary">No workspace</Typography.Text>;

  return (
    <Select
      value={currentWorkspace?.id}
      style={{ width: "100%" }}
      size="small"
      suffixIcon={<TeamOutlined />}
      onChange={(id) => {
        const ws = workspaces.find((w) => w.id === id);
        if (ws) setCurrentWorkspace(ws);
      }}
      options={workspaces.map((w) => ({
        label: w.name,
        value: w.id,
      }))}
    />
  );
};
