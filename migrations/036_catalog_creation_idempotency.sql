-- Recuperação dos cadastros de item, marca e produto.
-- Mesmo contrato das movimentações (003) e das contagens (020): cada criação
-- grava o identificador do envio e o hash da requisição. Um reenvio com o
-- mesmo identificador devolve o cadastro original; com outros dados, é recusado.
-- Linhas existentes continuam sem identificador (NULL) e seguem válidas.

ALTER TABLE inventory_items
    ADD COLUMN operation_id text,
    ADD COLUMN request_hash text;

ALTER TABLE inventory_items
    ADD CONSTRAINT inventory_items_operation_pair
    CHECK (
        (operation_id IS NULL AND request_hash IS NULL)
        OR (operation_id IS NOT NULL AND btrim(operation_id) <> '' AND request_hash IS NOT NULL AND btrim(request_hash) <> '')
    );

CREATE UNIQUE INDEX inventory_items_operation_id_unique
    ON inventory_items(operation_id)
    WHERE operation_id IS NOT NULL;

ALTER TABLE brands
    ADD COLUMN operation_id text,
    ADD COLUMN request_hash text;

ALTER TABLE brands
    ADD CONSTRAINT brands_operation_pair
    CHECK (
        (operation_id IS NULL AND request_hash IS NULL)
        OR (operation_id IS NOT NULL AND btrim(operation_id) <> '' AND request_hash IS NOT NULL AND btrim(request_hash) <> '')
    );

CREATE UNIQUE INDEX brands_operation_id_unique
    ON brands(operation_id)
    WHERE operation_id IS NOT NULL;

ALTER TABLE product_skus
    ADD COLUMN operation_id text,
    ADD COLUMN request_hash text;

ALTER TABLE product_skus
    ADD CONSTRAINT product_skus_operation_pair
    CHECK (
        (operation_id IS NULL AND request_hash IS NULL)
        OR (operation_id IS NOT NULL AND btrim(operation_id) <> '' AND request_hash IS NOT NULL AND btrim(request_hash) <> '')
    );

CREATE UNIQUE INDEX product_skus_operation_id_unique
    ON product_skus(operation_id)
    WHERE operation_id IS NOT NULL;
