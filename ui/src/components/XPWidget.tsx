import { useEffect, useState } from "react";
import { Progress, Tooltip, Spin, Typography } from "antd";
import { TrophyOutlined } from "@ant-design/icons";
import { xpService } from "@/services/api";
import { useWorkspace } from "@/contexts/WorkspaceContext";
import type { XPStatus, XPLevel } from "@/config";

export const XPWidget: React.FC = () => {
  const { currentWorkspace } = useWorkspace();
  const [xpStatus, setXpStatus] = useState<XPStatus | null>(null);
  const [levels, setLevels] = useState<XPLevel[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!currentWorkspace?.id) return;

    setLoading(true);
    Promise.all([
      xpService.getMyXP(currentWorkspace.id),
      xpService.listLevels(),
    ])
      .then(([xpRes, levelsRes]) => {
        setXpStatus(xpRes.data);
        setLevels(levelsRes.data.levels ?? []);
      })
      .catch(() => {
        // silently ignore — XP endpoint may not exist yet
      })
      .finally(() => setLoading(false));
  }, [currentWorkspace?.id]);

  if (!currentWorkspace || loading) {
    return loading ? (
      <div style={{ padding: "4px 0", textAlign: "center" }}>
        <Spin size="small" />
      </div>
    ) : null;
  }

  if (!xpStatus) return null;

  const currentLevel = levels.find((l) => l.level === xpStatus.level);
  const nextLevel = levels.find((l) => l.level === xpStatus.level + 1);

  const currentLevelXp = currentLevel?.xp_required ?? 0;
  const nextLevelXp = nextLevel?.xp_required ?? null;

  const progressPercent =
    nextLevelXp !== null && nextLevelXp > currentLevelXp
      ? Math.min(
          100,
          Math.round(
            ((xpStatus.xp - currentLevelXp) / (nextLevelXp - currentLevelXp)) * 100
          )
        )
      : 100;

  const levelTitle = currentLevel?.title ?? `Level ${xpStatus.level}`;

  return (
    <Tooltip
      title={
        nextLevelXp !== null
          ? `${xpStatus.xp} / ${nextLevelXp} XP`
          : `${xpStatus.xp} XP — max level`
      }
      placement="right"
    >
      <div
        style={{
          marginTop: 8,
          padding: "6px 8px",
          background: "rgba(79,70,229,0.08)",
          borderRadius: 6,
          cursor: "default",
        }}
      >
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 4,
            marginBottom: 4,
          }}
        >
          <TrophyOutlined style={{ color: "#faad14", fontSize: 12 }} />
          <Typography.Text style={{ fontSize: 11, fontWeight: 600, color: "#4f46e5" }}>
            Lv.{xpStatus.level}
          </Typography.Text>
          <Typography.Text
            style={{ fontSize: 10, color: "#6366f1", flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
          >
            {levelTitle}
          </Typography.Text>
        </div>
        <Progress
          percent={progressPercent}
          showInfo={false}
          size="small"
          strokeColor="#6366f1"
          trailColor="rgba(99,102,241,0.15)"
          style={{ margin: 0 }}
        />
      </div>
    </Tooltip>
  );
};
