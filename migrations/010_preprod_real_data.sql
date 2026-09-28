ALTER TABLE inventory_items
    ALTER COLUMN minimum_stock DROP NOT NULL;

UPDATE units SET name=v.name, magnitude=v.magnitude, active=v.active
FROM (VALUES
    ('UN','Unidade','Contagem',true),
    ('CX','Caixa','Contagem',true),
    ('PCT','Pacote','Contagem',true),
    ('KG','Quilograma','Massa',true),
    ('G','Grama','Massa',true),
    ('L','Litro','Volume',true),
    ('mL','Mililitro','Volume',true),
    ('M','Metro','Comprimento',true)
) AS v(code,name,magnitude,active)
WHERE units.code=v.code;

INSERT INTO units (code,name,magnitude,active)
VALUES
    ('UN','Unidade','Contagem',true),
    ('CX','Caixa','Contagem',true),
    ('PCT','Pacote','Contagem',true),
    ('KG','Quilograma','Massa',true),
    ('G','Grama','Massa',true),
    ('L','Litro','Volume',true),
    ('mL','Mililitro','Volume',true),
    ('M','Metro','Comprimento',true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO categories (legacy_id,name,active)
VALUES
    ('CAT001','Alimentos',true),
    ('CAT002','Bebidas',true),
    ('CAT003','Higiene e Limpeza',true),
    ('CAT004','Descartáveis e Embalagens',true),
    ('CAT005','Outros',true)
ON CONFLICT (name) DO UPDATE SET legacy_id=EXCLUDED.legacy_id, active=EXCLUDED.active;

INSERT INTO inventory_items (legacy_id,name,category_id,unit_code,minimum_stock,active)
VALUES
    ('ITEM001','Creme Culinário',NULL,'KG',10,true),
    ('ITEM002','Café extraforte vácuo',NULL,'KG',5,true),
    ('ITEM003','Leite Integral',NULL,'L',20,true),
    ('ITEM004','Leite de Coco',NULL,'L',20,true),
    ('ITEM005','Óleo de Soja',NULL,'mL',100,true),
    ('ITEM006','Farinha Branca',NULL,'KG',20,true),
    ('ITEM007','Óleo de Dendê',NULL,'mL',9000,true),
    ('ITEM008','Farinha de trigo',NULL,'KG',20,true),
    ('ITEM009','Arroz Parboilizado',NULL,'KG',20,true),
    ('ITEM010','Purê de Batatas',NULL,'KG',8000,true),
    ('ITEM011','Maionese',NULL,'KG',10,true),
    ('ITEM012','Fermento Químico em Pó',NULL,'KG',1000,true),
    ('ITEM013','Macarrão Penne',NULL,'KG',5000,true),
    ('ITEM014','Macarrão Lasanha',NULL,'KG',5000,true),
    ('ITEM015','Feijão Carioca',NULL,'KG',100,true),
    ('ITEM016','Flocão de Milho',NULL,'KG',5000,true),
    ('ITEM017','Feijão Preto',NULL,'KG',5000,true),
    ('ITEM018','Alho roxo em polpa',NULL,'KG',50,true),
    ('ITEM019','Macarrão Espaguete',NULL,'KG',50,true),
    ('8e6f0053','Água Mineral',NULL,'L',NULL,true)
ON CONFLICT (legacy_id) DO UPDATE SET
    name=EXCLUDED.name,
    category_id=NULL,
    unit_code=EXCLUDED.unit_code,
    minimum_stock=EXCLUDED.minimum_stock,
    active=EXCLUDED.active;

INSERT INTO product_skus (legacy_id,item_id,brand,name,barcode,active)
SELECT v.legacy_id, i.id, NULL, v.name, v.barcode, true
FROM (VALUES
    ('9 bf309ed','7898932481010','Creme Culinário - Daus','ITEM001'),
    ('7738001b','7891910030460','Café extraforte vácuo - União','ITEM002'),
    ('cd20205f','7896466621353','Leite Integral - Lírio','ITEM003'),
    ('73190094','7896016603891','Leite de Coco - Ducoco','ITEM004'),
    ('3ff34873','7898247780006','Óleo de Soja - ABC','ITEM005'),
    ('52d5e762','7898247780297','Óleo de soja - Vitaliv','ITEM005'),
    ('e69ace64','7898918167204','Farinha Branca - FIRMEZA','ITEM006'),
    ('aa1e67c4','7896369612076','Óleo de Dendê - Mariza','ITEM007'),
    ('c322522f','7896005276723','Farinha de trigo - Brandini','ITEM008'),
    ('6c8fe230','7897172500208','Arroz Parboilizado - Panelaço','ITEM009'),
    ('7daf504b','7896079441119','Arroz Parboilizado - Namorado','ITEM009'),
    ('907565f6','7898286805968','Purê de Batatas - Tecnutri','ITEM010'),
    ('3b575667','7891999015648','Maionese - Vigor','ITEM011'),
    ('cf2e0c33','7622300119621','Fermento Químico em Pó - Royal','ITEM012'),
    ('c80dd106','7896005214350','Macarrão Penne - Petybon','ITEM013'),
    ('14f13dc6','7896005299913','Macarrão Lasanha - Brandini','ITEM014'),
    ('1b7e4e7f','7898918167242','Feijão Carioca - Majestosa','ITEM015'),
    ('42b43f21','7898918167181','Feijão Carioca - Firmeza','ITEM015'),
    ('7536b008','7898029659452','Flocao de Milho - Lara','ITEM016'),
    ('add4778f','618231919802','Feijão Preto - Nota Dez','ITEM017'),
    ('52fbb4fd','7898950450012','Alho roxo em polpa - Alimempro','ITEM018'),
    ('75153cf9','17896005210199','Macarrao Espaguete - Favorita','ITEM019'),
    ('dba08c71','7898906878013','Agua Mineral - Ouro Azul','8e6f0053')
) AS v(legacy_id,barcode,name,item_legacy_id)
JOIN inventory_items i ON i.legacy_id=v.item_legacy_id
ON CONFLICT (legacy_id) DO UPDATE SET
    item_id=EXCLUDED.item_id,
    name=EXCLUDED.name,
    barcode=EXCLUDED.barcode,
    active=true;

INSERT INTO movements (legacy_id,operation_id,request_hash,movement_type,occurred_at,document_number,responsible,notes)
VALUES ('e1bfd712','legacy:e1bfd712','legacy:e1bfd712:v1','ENTRADA','2026-09-23 12:51:55 America/Sao_Paulo',NULL,'Davi',NULL)
ON CONFLICT (legacy_id) DO NOTHING;

INSERT INTO movement_lines (movement_id,sku_id,warehouse_id,direction,quantity,unit_cost)
SELECT m.id, s.id, w.id, 'IN', 5, 12
FROM movements m
JOIN product_skus s ON s.legacy_id='7738001b' AND s.barcode='7891910030460'
JOIN warehouses w ON w.name='Totalfood Estoque Seco'
WHERE m.legacy_id='e1bfd712'
  AND NOT EXISTS (SELECT 1 FROM movement_lines ml WHERE ml.movement_id=m.id);

DO $$
DECLARE
    n integer;
    cafe_sku bigint;
    seco bigint;
    saldo numeric;
    valor numeric;
    custo numeric;
BEGIN
    SELECT count(*) INTO n FROM units WHERE code IN ('UN','CX','PCT','KG','G','L','mL','M') AND active=true;
    IF n <> 8 THEN RAISE EXCEPTION 'preprod units invariant failed: %', n; END IF;

    SELECT count(*) INTO n FROM categories WHERE legacy_id IN ('CAT001','CAT002','CAT003','CAT004','CAT005') AND active=true;
    IF n <> 5 THEN RAISE EXCEPTION 'preprod categories invariant failed: %', n; END IF;

    SELECT count(*) INTO n FROM inventory_items WHERE legacy_id IN ('ITEM001','ITEM002','ITEM003','ITEM004','ITEM005','ITEM006','ITEM007','ITEM008','ITEM009','ITEM010','ITEM011','ITEM012','ITEM013','ITEM014','ITEM015','ITEM016','ITEM017','ITEM018','ITEM019','8e6f0053');
    IF n <> 20 THEN RAISE EXCEPTION 'preprod items invariant failed: %', n; END IF;

    SELECT count(*), count(DISTINCT barcode) INTO n, saldo FROM product_skus WHERE legacy_id IN ('9 bf309ed','7738001b','cd20205f','73190094','3ff34873','52d5e762','e69ace64','aa1e67c4','c322522f','6c8fe230','7daf504b','907565f6','3b575667','cf2e0c33','c80dd106','14f13dc6','1b7e4e7f','42b43f21','7536b008','add4778f','52fbb4fd','75153cf9','dba08c71');
    IF n <> 23 OR saldo <> 23 THEN RAISE EXCEPTION 'preprod skus invariant failed: % / %', n, saldo; END IF;

    SELECT id INTO cafe_sku FROM product_skus WHERE legacy_id='7738001b' AND barcode='7891910030460';
    SELECT id INTO seco FROM warehouses WHERE name='Totalfood Estoque Seco';
    IF cafe_sku IS NULL OR seco IS NULL THEN RAISE EXCEPTION 'preprod cafe/warehouse lookup failed'; END IF;

    SELECT count(*) INTO n FROM movements m JOIN movement_lines ml ON ml.movement_id=m.id WHERE m.legacy_id='e1bfd712' AND ml.sku_id=cafe_sku AND ml.warehouse_id=seco AND ml.direction='IN' AND ml.quantity=5 AND ml.unit_cost=12;
    IF n <> 1 THEN RAISE EXCEPTION 'preprod movement invariant failed: %', n; END IF;

    SELECT COALESCE(sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0), COALESCE(sum(CASE WHEN direction='IN' THEN quantity*unit_cost ELSE -quantity*unit_cost END),0)
    INTO saldo, valor
    FROM movement_lines WHERE sku_id=cafe_sku AND warehouse_id=seco;
    custo := CASE WHEN saldo = 0 THEN 0 ELSE valor / saldo END;
    IF saldo <> 5 OR valor <> 60 OR custo <> 12 THEN RAISE EXCEPTION 'preprod cafe stock invariant failed saldo=% valor=% custo=%', saldo, valor, custo; END IF;

    SELECT count(*) INTO n
    FROM product_skus s
    JOIN inventory_items i ON i.id=s.item_id
    JOIN movement_lines ml ON ml.sku_id=s.id
    WHERE i.legacy_id IN ('ITEM001','ITEM003','ITEM004','ITEM005','ITEM006','ITEM007','ITEM008','ITEM009','ITEM010','ITEM011','ITEM012','ITEM013','ITEM014','ITEM015','ITEM016','ITEM017','ITEM018','ITEM019','8e6f0053');
    IF n <> 0 THEN RAISE EXCEPTION 'preprod unexpected stock movements for other items: %', n; END IF;
END $$;
