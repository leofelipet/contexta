-- +goose Up
CREATE TABLE companies (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    notes TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX companies_name_key ON companies (lower(name));

ALTER TABLE tasks ADD COLUMN company_id BIGINT REFERENCES companies(id) ON DELETE SET NULL;
ALTER TABLE task_schedules ADD COLUMN company_id BIGINT REFERENCES companies(id) ON DELETE SET NULL;
ALTER TABLE contacts ADD COLUMN company_id BIGINT REFERENCES companies(id) ON DELETE SET NULL;
CREATE INDEX tasks_company_id_idx ON tasks (company_id);
CREATE INDEX task_schedules_company_id_idx ON task_schedules (company_id);
CREATE INDEX contacts_company_id_idx ON contacts (company_id);

-- Turn the free-text company names into companies. Spelling variants that differ
-- only by case collapse into one company, keeping the first spelling seen.
INSERT INTO companies (name)
SELECT DISTINCT ON (lower(name)) name
FROM (
    SELECT btrim(company) AS name, created_at FROM tasks WHERE btrim(company) <> ''
    UNION ALL
    SELECT btrim(company) AS name, created_at FROM task_schedules WHERE btrim(company) <> ''
) names
ORDER BY lower(name), created_at;

UPDATE tasks t SET company_id = c.id
FROM companies c
WHERE btrim(t.company) <> '' AND lower(btrim(t.company)) = lower(c.name);

UPDATE task_schedules s SET company_id = c.id
FROM companies c
WHERE btrim(s.company) <> '' AND lower(btrim(s.company)) = lower(c.name);

DROP INDEX IF EXISTS tasks_company_idx;
ALTER TABLE tasks DROP COLUMN company;
ALTER TABLE task_schedules DROP COLUMN company;

-- +goose Down
ALTER TABLE tasks ADD COLUMN company TEXT NOT NULL DEFAULT '';
ALTER TABLE task_schedules ADD COLUMN company TEXT NOT NULL DEFAULT '';
CREATE INDEX tasks_company_idx ON tasks (company);

UPDATE tasks t SET company = c.name FROM companies c WHERE c.id = t.company_id;
UPDATE task_schedules s SET company = c.name FROM companies c WHERE c.id = s.company_id;

ALTER TABLE contacts DROP COLUMN IF EXISTS company_id;
ALTER TABLE task_schedules DROP COLUMN IF EXISTS company_id;
ALTER TABLE tasks DROP COLUMN IF EXISTS company_id;
DROP TABLE IF EXISTS companies;
