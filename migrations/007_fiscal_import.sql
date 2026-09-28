INSERT INTO permissions (resource, action)
VALUES ('fiscal_import', 'write');

INSERT INTO profile_permissions (profile_id, permission_id)
SELECT p.id, perm.id
FROM profiles p
JOIN permissions perm ON perm.resource = 'fiscal_import' AND perm.action = 'write'
WHERE p.name = 'Diretor';
