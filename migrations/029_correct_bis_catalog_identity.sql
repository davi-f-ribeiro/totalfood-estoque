DO $$
DECLARE
    v_sku_id bigint;
    v_item_id bigint;
    v_brand_id bigint;
    v_item_name text;
    v_brand_name text;
    v_name text;
    v_bis_brand_id bigint;
BEGIN
    SELECT s.id,s.item_id,s.brand_id,i.name,b.name,COALESCE(s.name,'')
      INTO v_sku_id,v_item_id,v_brand_id,v_item_name,v_brand_name,v_name
      FROM product_skus s
      JOIN inventory_items i ON i.id=s.item_id
      JOIN brands b ON b.id=s.brand_id
     WHERE s.barcode='7622210576071'
     LIMIT 1;

    IF NOT FOUND THEN
        RETURN;
    END IF;

    IF v_item_name='Chocolate' AND v_brand_name='BIS' AND v_name='Original' THEN
        RETURN;
    END IF;

    IF v_item_name<>'BIS - Original' OR v_brand_name<>'Original' THEN
        RAISE EXCEPTION 'BIS correction stopped: unexpected catalog state item=%, brand=%, product=%',
            v_item_name,v_brand_name,v_name;
    END IF;

    IF EXISTS (SELECT 1 FROM movement_lines WHERE sku_id=v_sku_id)
       OR EXISTS (SELECT 1 FROM movement_lines WHERE item_id=v_item_id)
       OR EXISTS (SELECT 1 FROM stock_lots WHERE item_id=v_item_id) THEN
        RAISE EXCEPTION 'BIS correction stopped: catalog identity already has stock history';
    END IF;

    IF EXISTS (
        SELECT 1 FROM inventory_items
        WHERE id<>v_item_id AND lower(btrim(name))=lower('Chocolate')
    ) THEN
        RAISE EXCEPTION 'BIS correction stopped: Chocolate parent already exists and requires explicit reconciliation';
    END IF;

    UPDATE inventory_items
       SET name='Chocolate',version=version+1
     WHERE id=v_item_id;

    INSERT INTO brands(name,active)
    SELECT 'BIS',true
    WHERE NOT EXISTS (SELECT 1 FROM brands WHERE lower(btrim(name))=lower('BIS'));

    SELECT id INTO v_bis_brand_id
      FROM brands
     WHERE lower(btrim(name))=lower('BIS')
     ORDER BY id
     LIMIT 1;

    UPDATE product_skus
       SET brand_id=v_bis_brand_id,
           name='Original',
           version=version+1
     WHERE id=v_sku_id;
END $$;
