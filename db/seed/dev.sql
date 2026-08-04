-- RDS 결정론적 시드 (PLAN §결정론 상수).
-- UUID 는 앞 블록으로 테이블을 구분하는 고정값이다:
--   aaaaaaaa=tenants  bbbbbbbb=teams  cccccccc=members  dddddddd=team_memberships
--   eeeeeeee=invitations  ffffffff=installations  11111111=credentials  22222222=manifests
-- 구버전 시드와 시나리오를 유지한다:
--   * Bob 은 팀 이동 이력 2행: Backend [2020-01-01, 2026-06-01) → Platform [2026-06-01, NULL). 인접(겹침·공백 없음).
--   * 미등록 사용자(구 emp-404)는 members 에 넣지 않는다.

INSERT INTO enrollment.tenants (id, name, slug, timezone, status) VALUES
  ('aaaaaaaa-0000-0000-0000-000000000001', 'ACME Corp',  'acme',   'Asia/Seoul', 'active'),
  ('aaaaaaaa-0000-0000-0000-000000000002', 'Globex Inc', 'globex', 'Asia/Seoul', 'active');

INSERT INTO enrollment.teams (id, tenant_id, name, status) VALUES
  ('bbbbbbbb-0000-0000-0000-000000000001', 'aaaaaaaa-0000-0000-0000-000000000001', 'Platform', 'active'),
  ('bbbbbbbb-0000-0000-0000-000000000002', 'aaaaaaaa-0000-0000-0000-000000000001', 'Backend',  'active'),
  ('bbbbbbbb-0000-0000-0000-000000000003', 'aaaaaaaa-0000-0000-0000-000000000002', 'Data',     'active');

-- 관리자(웹 사용자)는 cognito_user_sub 연결, 일반 사용자는 NULL(설치로만 연결).
INSERT INTO enrollment.members (id, tenant_id, cognito_user_sub, email, display_name, role, status) VALUES
  ('cccccccc-0000-0000-0000-000000000001', 'aaaaaaaa-0000-0000-0000-000000000001', 'cognito-acme-admin',   'admin@acme.test',   'Acme Admin',   'owner',  'active'),
  ('cccccccc-0000-0000-0000-000000000002', 'aaaaaaaa-0000-0000-0000-000000000001', NULL,                   'alice@acme.test',   'Alice',        'member', 'active'),
  ('cccccccc-0000-0000-0000-000000000003', 'aaaaaaaa-0000-0000-0000-000000000001', NULL,                   'bob@acme.test',     'Bob',          'member', 'active'),
  ('cccccccc-0000-0000-0000-000000000004', 'aaaaaaaa-0000-0000-0000-000000000002', 'cognito-globex-admin', 'admin@globex.test', 'Globex Admin', 'owner',  'active'),
  ('cccccccc-0000-0000-0000-000000000005', 'aaaaaaaa-0000-0000-0000-000000000002', NULL,                   'carol@globex.test', 'Carol',        'member', 'active');
  -- 미등록 사용자: 없음(구 emp-404 시나리오)

INSERT INTO enrollment.team_memberships (id, team_id, member_id, joined_at, left_at) VALUES
  ('dddddddd-0000-0000-0000-000000000001', 'bbbbbbbb-0000-0000-0000-000000000001', 'cccccccc-0000-0000-0000-000000000002', TIMESTAMPTZ '2020-01-01T00:00:00Z', NULL),
  ('dddddddd-0000-0000-0000-000000000002', 'bbbbbbbb-0000-0000-0000-000000000002', 'cccccccc-0000-0000-0000-000000000003', TIMESTAMPTZ '2020-01-01T00:00:00Z', TIMESTAMPTZ '2026-06-01T00:00:00Z'),
  ('dddddddd-0000-0000-0000-000000000003', 'bbbbbbbb-0000-0000-0000-000000000001', 'cccccccc-0000-0000-0000-000000000003', TIMESTAMPTZ '2026-06-01T00:00:00Z', NULL),
  ('dddddddd-0000-0000-0000-000000000004', 'bbbbbbbb-0000-0000-0000-000000000003', 'cccccccc-0000-0000-0000-000000000005', TIMESTAMPTZ '2020-01-01T00:00:00Z', NULL);

-- 초대: 사용됨 2건(설치로 이어짐) + 미사용 1건 + 폐기 1건.
-- e-0003 is HMAC-SHA256(dev-only-invite-pepper, TEST-1234).
INSERT INTO enrollment.invitations (id, tenant_id, target_member_id, created_by_member_id, code_hash, used_at, expires_at, revoked_at) VALUES
  ('eeeeeeee-0000-0000-0000-000000000001', 'aaaaaaaa-0000-0000-0000-000000000001', 'cccccccc-0000-0000-0000-000000000002', 'cccccccc-0000-0000-0000-000000000001', 'hash-inv-alice-1', TIMESTAMPTZ '2026-07-01T09:00:00Z', TIMESTAMPTZ '2026-07-08T00:00:00Z', NULL),
  ('eeeeeeee-0000-0000-0000-000000000002', 'aaaaaaaa-0000-0000-0000-000000000001', 'cccccccc-0000-0000-0000-000000000003', 'cccccccc-0000-0000-0000-000000000001', 'hash-inv-bob-1',   TIMESTAMPTZ '2026-07-02T09:00:00Z', TIMESTAMPTZ '2026-07-09T00:00:00Z', NULL),
  ('eeeeeeee-0000-0000-0000-000000000003', 'aaaaaaaa-0000-0000-0000-000000000001', 'cccccccc-0000-0000-0000-000000000003', 'cccccccc-0000-0000-0000-000000000001', 'ccea66a3034fb3695c25c01731ea448c24b4d744b9e3ee693967c9f5d05dcc22', NULL, TIMESTAMPTZ '2026-12-31T00:00:00Z', NULL),
  ('eeeeeeee-0000-0000-0000-000000000004', 'aaaaaaaa-0000-0000-0000-000000000002', 'cccccccc-0000-0000-0000-000000000005', 'cccccccc-0000-0000-0000-000000000004', 'hash-inv-carol-1', TIMESTAMPTZ '2026-07-03T09:00:00Z', TIMESTAMPTZ '2026-07-10T00:00:00Z', NULL),
  ('eeeeeeee-0000-0000-0000-000000000005', 'aaaaaaaa-0000-0000-0000-000000000002', 'cccccccc-0000-0000-0000-000000000005', 'cccccccc-0000-0000-0000-000000000004', 'hash-inv-carol-2', NULL,                               TIMESTAMPTZ '2026-12-31T00:00:00Z', TIMESTAMPTZ '2026-07-05T00:00:00Z');

-- 설치: active 2건 + revoked 1건(Carol — 폐기 시나리오).
INSERT INTO enrollment.installations (id, tenant_id, member_id, invitation_id, hostname, platform, architecture, client_version, status, last_seen_at, revoked_at) VALUES
  ('ffffffff-0000-0000-0000-000000000001', 'aaaaaaaa-0000-0000-0000-000000000001', 'cccccccc-0000-0000-0000-000000000002', 'eeeeeeee-0000-0000-0000-000000000001', 'alice-laptop', 'windows', 'amd64', '0.9.2', 'active',  TIMESTAMPTZ '2026-08-01T12:00:00Z', NULL),
  ('ffffffff-0000-0000-0000-000000000002', 'aaaaaaaa-0000-0000-0000-000000000001', 'cccccccc-0000-0000-0000-000000000003', 'eeeeeeee-0000-0000-0000-000000000002', 'bob-macbook',  'macos',   'arm64', '0.9.2', 'active',  TIMESTAMPTZ '2026-08-03T15:30:00Z', NULL),
  ('ffffffff-0000-0000-0000-000000000003', 'aaaaaaaa-0000-0000-0000-000000000002', 'cccccccc-0000-0000-0000-000000000005', 'eeeeeeee-0000-0000-0000-000000000004', 'carol-desk',   'linux',   'amd64', '0.9.1', 'revoked', TIMESTAMPTZ '2026-07-19T10:00:00Z', TIMESTAMPTZ '2026-07-20T00:00:00Z');

-- 자격증명: Bob 은 rotation 이력(폐기 1 + 활성 1), Carol 은 설치 폐기와 함께 폐기.
INSERT INTO enrollment.installation_credentials (id, installation_id, credential_hash, issued_at, last_used_at, revoked_at) VALUES
  ('11111111-0000-0000-0000-000000000001', 'ffffffff-0000-0000-0000-000000000001', 'cred-hash-alice-1', TIMESTAMPTZ '2026-07-01T09:05:00Z', TIMESTAMPTZ '2026-08-01T12:00:00Z', NULL),
  ('11111111-0000-0000-0000-000000000002', 'ffffffff-0000-0000-0000-000000000002', 'cred-hash-bob-1',   TIMESTAMPTZ '2026-07-02T09:05:00Z', TIMESTAMPTZ '2026-07-15T08:00:00Z', TIMESTAMPTZ '2026-07-15T08:00:00Z'),
  ('11111111-0000-0000-0000-000000000003', 'ffffffff-0000-0000-0000-000000000002', 'cred-hash-bob-2',   TIMESTAMPTZ '2026-07-15T08:00:00Z', TIMESTAMPTZ '2026-08-03T15:30:00Z', NULL),
  ('11111111-0000-0000-0000-000000000004', 'ffffffff-0000-0000-0000-000000000003', 'cred-hash-carol-1', TIMESTAMPTZ '2026-07-03T09:05:00Z', TIMESTAMPTZ '2026-07-19T10:00:00Z', TIMESTAMPTZ '2026-07-20T00:00:00Z');

-- manifest 이력: ACME 는 v1(비활성, 과거 적용) → v2(활성). Globex 는 v1(활성).
INSERT INTO enrollment.manifests (id, tenant_id, version, manifest, is_active, created_by_member_id, activated_at) VALUES
  ('22222222-0000-0000-0000-000000000001', 'aaaaaaaa-0000-0000-0000-000000000001', 1,
   '{"schema_version": 1, "config_revision": 1, "otlp": {"endpoint": "https://telemetry.acme.test", "protocol": "http/protobuf"}, "signals": {"logs": true, "metrics": true, "traces": false}, "privacy": {"collect_user_prompts": false}}',
   false, 'cccccccc-0000-0000-0000-000000000001', TIMESTAMPTZ '2026-07-01T00:00:00Z'),
  ('22222222-0000-0000-0000-000000000002', 'aaaaaaaa-0000-0000-0000-000000000001', 2,
   '{"schema_version": 1, "config_revision": 2, "otlp": {"endpoint": "https://telemetry.acme.test", "protocol": "http/protobuf", "compression": "gzip"}, "signals": {"logs": true, "metrics": true, "traces": false}, "privacy": {"collect_user_prompts": false}}',
   true,  'cccccccc-0000-0000-0000-000000000001', TIMESTAMPTZ '2026-07-15T00:00:00Z'),
  ('22222222-0000-0000-0000-000000000003', 'aaaaaaaa-0000-0000-0000-000000000002', 1,
   '{"schema_version": 1, "config_revision": 1, "otlp": {"endpoint": "https://telemetry.globex.test", "protocol": "http/protobuf"}, "signals": {"logs": true, "metrics": false, "traces": false}, "privacy": {"collect_user_prompts": false}}',
   true,  'cccccccc-0000-0000-0000-000000000004', TIMESTAMPTZ '2026-07-03T00:00:00Z');

-- 배포 상태: Alice 는 v1 적용 이력 + v2 적용 완료, Bob 은 v2 배포됐지만 미적용(drift 시나리오).
INSERT INTO enrollment.installation_manifest_assignments (installation_id, manifest_id, assigned_at, applied_at) VALUES
  ('ffffffff-0000-0000-0000-000000000001', '22222222-0000-0000-0000-000000000001', TIMESTAMPTZ '2026-07-01T09:10:00Z', TIMESTAMPTZ '2026-07-01T09:10:30Z'),
  ('ffffffff-0000-0000-0000-000000000001', '22222222-0000-0000-0000-000000000002', TIMESTAMPTZ '2026-07-15T00:05:00Z', TIMESTAMPTZ '2026-07-15T00:05:20Z'),
  ('ffffffff-0000-0000-0000-000000000002', '22222222-0000-0000-0000-000000000002', TIMESTAMPTZ '2026-07-15T00:05:00Z', NULL),
  ('ffffffff-0000-0000-0000-000000000003', '22222222-0000-0000-0000-000000000003', TIMESTAMPTZ '2026-07-03T09:10:00Z', TIMESTAMPTZ '2026-07-03T09:10:30Z');
