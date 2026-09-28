ALTER TABLE inventory_items
    ADD COLUMN notes text,
    ADD CONSTRAINT inventory_items_notes_not_blank CHECK (notes IS NULL OR btrim(notes) <> '');

INSERT INTO permissions (resource, action)
VALUES ('catalog', 'read'), ('catalog', 'write');

INSERT INTO profile_permissions (profile_id, permission_id)
SELECT p.id, perm.id
FROM profiles p
JOIN permissions perm ON perm.resource = 'catalog'
WHERE p.name = 'Diretor';

CREATE INDEX idx_inventory_items_name ON inventory_items(name);
CREATE INDEX idx_product_skus_name ON product_skus(name);
