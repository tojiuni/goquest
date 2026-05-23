-- workspace name과 project name(워크스페이스 내)을 대소문자 무시 유일로 승격.
-- 이름 기반 해석이 LOWER(name) 매칭이므로 CI 유일성이 DB로 보장돼야 임의 행 반환을 막는다.
-- 적용 전 사전 점검(런북): 중복이 있으면 인덱스 생성이 실패한다.
--   SELECT LOWER(name), COUNT(*) FROM workspaces GROUP BY 1 HAVING COUNT(*) > 1;
--   SELECT workspace_id, LOWER(name), COUNT(*) FROM projects GROUP BY 1,2 HAVING COUNT(*) > 1;
CREATE UNIQUE INDEX IF NOT EXISTS workspaces_lower_name_key
  ON workspaces (LOWER(name));

CREATE UNIQUE INDEX IF NOT EXISTS projects_workspace_lower_name_key
  ON projects (workspace_id, LOWER(name));
