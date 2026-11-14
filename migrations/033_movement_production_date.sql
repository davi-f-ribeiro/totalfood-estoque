-- Data de consumo/produção, independente do instante real da saída.
-- NULL preserva o significado legado de saídas anteriores à implantação.
ALTER TABLE movements
    ADD COLUMN production_date date;

ALTER TABLE movements
    ADD CONSTRAINT movements_production_date_only_exit
    CHECK (production_date IS NULL OR movement_type = 'SAIDA');
