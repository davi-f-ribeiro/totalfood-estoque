-- Etiqueta de teste da impressora: um trabalho de impressão sem caixa
-- (crate_id NULL), sempre endereçado a uma impressora específica.
--
-- Deve aplicar tanto sobre a 039 original quanto sobre um banco em que
-- crate_id já foi tornado opcional à mão e há trabalho sem caixa criado fora
-- do sistema. Esse trabalho tem ZPL desconhecido: é cancelado, nunca impresso
-- e não é apagado, para ficar no histórico.
ALTER TABLE print_jobs ALTER COLUMN crate_id DROP NOT NULL;

UPDATE print_jobs
   SET status = 'CANCELLED',
       claim_token_hash = NULL,
       last_error = 'Trabalho sem caixa criado fora do sistema; cancelado pela migration 040'
 WHERE crate_id IS NULL
   AND status IN ('PENDING', 'CLAIMED', 'UNCERTAIN', 'FAILED');

-- Trabalho sem caixa ativo só existe como teste de uma impressora. Registros
-- encerrados (enviados ou cancelados) ficam aceitos como estão.
ALTER TABLE print_jobs
  ADD CONSTRAINT print_jobs_test_targets_device
  CHECK (crate_id IS NOT NULL OR device_id IS NOT NULL OR status IN ('SENT', 'CANCELLED'));
