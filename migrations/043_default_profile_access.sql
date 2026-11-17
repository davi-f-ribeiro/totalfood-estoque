-- Gerente e Estoquista nasciam só com Requisições: nenhuma migration anterior deu a eles
-- as telas de estoque, e um banco novo deixava os dois perfis sem ter como trabalhar.
-- Estoquista: ver estoque e catálogo, dar entrada, saída e transferir (inclui Caixas).
-- Gerente: ver estoque, catálogo e relatórios e contar o estoque.
-- O ajuste de estoque (stock_adjustment) continua sem perfil automático, como na migration 030:
-- quem ajusta é decidido pelo Diretor em Administração.
-- Só acrescenta acessos; o que o Diretor já configurou em Administração continua valendo.
INSERT INTO profile_permissions (profile_id, permission_id)
SELECT p.id, perm.id
FROM profiles p
JOIN permissions perm ON
     (p.name = 'Estoquista' AND (
          (perm.resource = 'stock' AND perm.action = 'read')
       OR (perm.resource = 'catalog' AND perm.action = 'read')
       OR (perm.resource IN ('movement_entry', 'movement_exit', 'movement_transfer') AND perm.action = 'write')))
  OR (p.name = 'Gerente' AND (
          (perm.resource = 'stock' AND perm.action = 'read')
       OR (perm.resource = 'catalog' AND perm.action = 'read')
       OR (perm.resource = 'operations' AND perm.action = 'read')
       OR (perm.resource = 'inventory_validation' AND perm.action IN ('read', 'write'))))
ON CONFLICT DO NOTHING;
