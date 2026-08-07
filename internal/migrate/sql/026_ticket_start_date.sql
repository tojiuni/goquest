-- start_at: roadmap viewer用 ticket start date (nullable). due_at は既存の
-- single deadline endpoint で、start_at はその反対側の end — both がある場合
-- roadmap bar は [start_at, due_at] range として描画され、start_at のみない場合は
-- due_at single point (point marker) として描画される (GQT-SPC-2026080702).
ALTER TABLE tickets ADD COLUMN start_at timestamptz NULL;
