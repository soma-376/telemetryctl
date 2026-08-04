-- enrollment 서버(Postgres) 스키마. docker-compose.dev.yml 이 /docker-entrypoint-initdb.d 로
-- 마운트해 데이터 디렉터리가 빌 때 자동 적용한다(01-schema → 02-seed 순).
-- named volume(pgdata) 사용 중이므로 재적용하려면 `docker compose down -v` 필요.
-- 원본 모델: DBML(enrollment.*) — 권위 소스는 이 파일.
-- Initial enrollment schema.
-- (docs/enrollment-server-spec.md §8).

CREATE SCHEMA enrollment;

CREATE TYPE enrollment.tenant_status       AS ENUM ('active', 'suspended', 'terminated');
CREATE TYPE enrollment.team_status         AS ENUM ('active', 'archived');
CREATE TYPE enrollment.member_role         AS ENUM ('owner', 'admin', 'member');
CREATE TYPE enrollment.member_status       AS ENUM ('invited', 'active', 'suspended');
CREATE TYPE enrollment.installation_status AS ENUM ('active', 'revoked');
CREATE TYPE enrollment.platform_type       AS ENUM ('windows', 'macos', 'linux');

CREATE TABLE enrollment.tenants (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    name       varchar(100) NOT NULL,
    slug       varchar(100) UNIQUE,

    timezone   varchar(50) NOT NULL DEFAULT 'Asia/Seoul',
    logo_url   text,

    status     enrollment.tenant_status NOT NULL DEFAULT 'active',

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
COMMENT ON TABLE enrollment.tenants IS 'Pulsemetry를 사용하는 고객 조직';

CREATE TABLE enrollment.teams (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    tenant_id  uuid NOT NULL REFERENCES enrollment.tenants(id),

    name       varchar(100) NOT NULL,
    status     enrollment.team_status NOT NULL DEFAULT 'active',

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    UNIQUE (tenant_id, name)
);
CREATE INDEX idx_teams_tenant_id ON enrollment.teams (tenant_id);
COMMENT ON TABLE enrollment.teams IS 'tenant 내부에서 구성원과 AI 사용량을 구분하기 위한 팀 또는 부서 단위';

CREATE TABLE enrollment.members (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid NOT NULL REFERENCES enrollment.tenants(id),

    cognito_user_sub varchar(255),
    email            varchar(320) NOT NULL,
    display_name     varchar(100),

    role             enrollment.member_role NOT NULL DEFAULT 'member',
    status           enrollment.member_status NOT NULL DEFAULT 'active',

    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),

    UNIQUE (tenant_id, cognito_user_sub),
    UNIQUE (tenant_id, email)
);
CREATE INDEX idx_members_cognito_user_sub ON enrollment.members (cognito_user_sub);
COMMENT ON TABLE enrollment.members IS 'Pulsemetry 조직 구성원. 관리자 등 웹 사용자는 Cognito 계정과 연결되며, 일반 사용자는 installation을 통해 서비스와 연결된다.';

CREATE TABLE enrollment.team_memberships (
    id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    team_id   uuid NOT NULL REFERENCES enrollment.teams(id),
    member_id uuid NOT NULL REFERENCES enrollment.members(id),

    joined_at timestamptz NOT NULL DEFAULT now(),
    left_at   timestamptz
);
CREATE INDEX idx_team_memberships_team_id   ON enrollment.team_memberships (team_id);
CREATE INDEX idx_team_memberships_member_id ON enrollment.team_memberships (member_id);
COMMENT ON TABLE enrollment.team_memberships IS '구성원의 팀 소속 관계와 소속 기간';

CREATE TABLE enrollment.invitations (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    tenant_id            uuid NOT NULL REFERENCES enrollment.tenants(id),
    target_member_id     uuid NOT NULL REFERENCES enrollment.members(id),
    created_by_member_id uuid NOT NULL REFERENCES enrollment.members(id),

    code_hash            varchar(255) NOT NULL UNIQUE,

    used_at              timestamptz,
    expires_at           timestamptz NOT NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    revoked_at           timestamptz
);
CREATE INDEX idx_invitations_tenant_id  ON enrollment.invitations (tenant_id);
CREATE INDEX idx_invitations_expires_at ON enrollment.invitations (expires_at);
COMMENT ON TABLE enrollment.invitations IS 'CLI 설치를 허용하는 일회성 초대 코드. 관리자가 생성하여 이메일 등으로 사용자에게 전달되며, 설치(enrollment)에 한 번만 사용된다.';

CREATE TABLE enrollment.installations (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    tenant_id      uuid NOT NULL REFERENCES enrollment.tenants(id),
    member_id      uuid NOT NULL REFERENCES enrollment.members(id),
    invitation_id  uuid NOT NULL REFERENCES enrollment.invitations(id),

    hostname       varchar(255),

    platform       enrollment.platform_type NOT NULL,
    architecture   varchar(30),
    client_version varchar(50),

    status         enrollment.installation_status NOT NULL DEFAULT 'active',

    last_seen_at   timestamptz,
    revoked_at     timestamptz,

    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_installations_member_id     ON enrollment.installations (member_id);
CREATE INDEX idx_installations_invitation_id ON enrollment.installations (invitation_id);
CREATE INDEX idx_installations_tenant_status ON enrollment.installations (tenant_id, status);
COMMENT ON TABLE enrollment.installations IS '사용자의 PC에 설치된 Pulsemetry CLI 또는 daemon';

CREATE TABLE enrollment.installation_credentials (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    installation_id uuid NOT NULL REFERENCES enrollment.installations(id),

    credential_hash varchar(255) NOT NULL UNIQUE,

    issued_at       timestamptz NOT NULL DEFAULT now(),
    last_used_at    timestamptz,
    revoked_at      timestamptz
);
CREATE INDEX idx_installation_credentials_installation_id
    ON enrollment.installation_credentials (installation_id);
COMMENT ON TABLE enrollment.installation_credentials IS 'installation이 OTLP 전송 및 개인 API 호출에 사용하는 인증 자격증명과 폐기 이력';

CREATE TABLE enrollment.manifests (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            uuid NOT NULL REFERENCES enrollment.tenants(id),

    version              int NOT NULL,
    manifest             jsonb NOT NULL,

    is_active            boolean NOT NULL DEFAULT false,

    created_by_member_id uuid NOT NULL REFERENCES enrollment.members(id),
    created_at           timestamptz NOT NULL DEFAULT now(),
    activated_at         timestamptz,

    UNIQUE (tenant_id, version)
);
CREATE INDEX idx_manifests_tenant_is_active ON enrollment.manifests (tenant_id, is_active);
COMMENT ON TABLE enrollment.manifests IS 'tenant별 수집 및 privacy 정책의 버전 이력. 기존 row는 수정하지 않고 설정 변경 시 새 version을 생성한다.';

CREATE TABLE enrollment.installation_manifest_assignments (
    installation_id uuid NOT NULL REFERENCES enrollment.installations(id),
    manifest_id     uuid NOT NULL REFERENCES enrollment.manifests(id),

    assigned_at     timestamptz NOT NULL DEFAULT now(),
    applied_at      timestamptz,

    PRIMARY KEY (installation_id, manifest_id)
);
CREATE INDEX idx_ima_manifest_id ON enrollment.installation_manifest_assignments (manifest_id);
COMMENT ON TABLE enrollment.installation_manifest_assignments IS 'installation에 배포된 manifest 버전과 적용 여부';
