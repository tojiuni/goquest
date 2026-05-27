-- workspace를 ZITADEL org에 연결(org-admin 하향 권한). NULL = org 미연결.
-- 값(어느 org)은 롤아웃 단계에서 실제 ZITADEL org id로 배정한다(코드 마이그레이션은 컬럼만).
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS org_id TEXT;
