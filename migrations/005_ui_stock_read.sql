INSERT INTO permissions (resource, action)
VALUES ('stock', 'read');

INSERT INTO profile_permissions (profile_id, permission_id)
SELECT p.id, perm.id
FROM profiles p
JOIN permissions perm ON perm.resource = 'stock' AND perm.action = 'read'
WHERE p.name = 'Diretor';

CREATE INDEX idx_product_skus_barcode ON product_skus(barcode);
