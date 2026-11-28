SET LOCAL lock_timeout = '2s';

ALTER TABLE cmp_project_audit DROP CONSTRAINT cmp_project_audit_category_check;
ALTER TABLE cmp_project_audit ADD CONSTRAINT cmp_project_audit_category_check
    CHECK (category IN ('account','ownership','operation','approval','resolution','observation',
                        'catalog','alert','console_access','billing_budget','tag_policy','change_ticket','billing_allocation')) NOT VALID;
