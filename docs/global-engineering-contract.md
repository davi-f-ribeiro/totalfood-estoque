# Contrato global de engenharia — TotalFood Estoque

Este documento define invariantes para toda manutenção e evolução do sistema. Regras específicas de um módulo podem complementar este contrato, mas não contradizê-lo sem decisão explícita e documentada.

## 1. Fonte única e responsabilidade

- Cada comportamento de domínio deve possuir uma responsabilidade canônica.
- Antes de criar função, endpoint, consulta, estado ou componente novo, verificar se uma responsabilidade existente pode ser reutilizada ou estendida com segurança.
- Não criar funções concorrentes para o mesmo comportamento por diferença de origem da ação, dispositivo ou tela. Ex.: câmera e leitor físico devem convergir para o mesmo resolvedor de produto.
- Frontend não replica regra financeira, cálculo de estoque, validação de domínio ou autorização que pertence ao backend.
- Uma representação derivada não se torna nova fonte de verdade. Cache, prévia, relatório e exportação devem derivar da fonte canônica.

## 2. Mudança mínima

- Alterar somente o necessário para cumprir o requisito comprovado.
- Função, tabela, coluna, migration, índice, dependência ou abstração nova é último recurso, não ponto de partida.
- Não refatorar áreas não relacionadas durante uma correção ou feature.
- Não adicionar índice por hipótese: exigir evidência de consulta/plano/performance que justifique a mudança.
- Não criar compatibilidade paralela permanente quando a responsabilidade existente puder ser corrigida.

## 3. Feedback e mensagens ao usuário

- Feedback deve permanecer próximo da ação/objeto que o originou. Toast global é reservado a eventos realmente globais.
- Estados de processamento, sucesso e erro devem ser visíveis sem exigir que o usuário procure outra região da página.
- Sucesso só pode ser mostrado após confirmação do backend; não usar sucesso otimista para operações persistentes.
- Mensagens devem descrever ação e objeto reais. Evitar mensagens genéricas como "Operação concluída", "Erro ao processar", "Item não criado" ou "Revise o formulário" quando o sistema conhece a causa.
- Erros de validação, conflito/duplicidade, permissão, rede e falha interna devem ser distinguíveis.
- Mensagens de domínio válidas retornadas pelo backend não devem ser descartadas por fallback genérico no frontend.
- SQL, stack trace, constraint interna e detalhes técnicos não são exibidos ao usuário; ficam em logs.
- Em erro corrigível, preservar dados preenchidos necessários à correção. Em sucesso, limpar ou atualizar somente o estado previsto pelo fluxo.

## 4. Estado e UX

- Estado visual deve corresponder ao estado real da operação. Ao mudar filtros/seleção que invalidem um resultado, a UI deve invalidar ou atualizar esse resultado.
- Feedback, detalhe, sanfona e confirmação relacionados a um item devem acompanhar o item/ação selecionados e permanecer no contexto visual do usuário.
- A mesma ação por dispositivos diferentes deve produzir o mesmo resultado funcional e a mesma classe de feedback.
- Evitar seleção repetida de uma informação que o sistema já conhece.
- Controles indisponíveis ou sem efeito não devem aparentar estar operacionais.
- Estados selecionados devem ser perceptíveis e, quando aplicável, representados semanticamente para acessibilidade.
- Fluxos devem continuar responsivos nas larguras suportadas pelo sistema.

## 5. Erros, concorrência e idempotência

- Conflitos de edição concorrente devem ser detectados no backend e informados de forma recuperável; não sobrescrever silenciosamente alterações de outro operador.
- Operações que já possuem mecanismo de idempotência devem reutilizá-lo; não criar identificador concorrente.
- Falha de rede com resultado incerto não pode ser tratada automaticamente como falha da operação persistente quando existe mecanismo de verificação/idempotência.
- Restrições de integridade relevantes devem existir no banco quando apropriado e também produzir mensagem de domínio compreensível.

## 6. Autorização

- Autorização é responsabilidade do backend.
- Frontend pode ocultar ações sem permissão para melhorar UX, mas isso nunca substitui proteção do endpoint.
- Reutilizar permissões existentes quando a semântica for compatível. Nova permissão exige necessidade real e análise de impacto nos perfis.
- Ao ampliar dados expostos por uma tela/endpoint, reavaliar se a permissão existente continua adequada, especialmente para custos, dados administrativos e histórico operacional.

## 7. Banco e migrations

- Ledger e estruturas canônicas existentes devem ser reutilizados antes de materializar estados derivados.
- Migration deve ser necessária, reversível/operacionalmente segura dentro das práticas do projeto e testada no mesmo mecanismo usado pelo CI.
- Evitar DDL misturado a operações que possam deixar eventos de trigger pendentes ou introduzir risco conhecido de deploy.
- Alteração de schema não deve ser usada para resolver problema exclusivamente de apresentação.
- Compatibilidade com dados legados deve ser preservada quando esses dados continuam válidos.

## 8. Relatórios e exportações

- Prévia e exportação do mesmo relatório devem compartilhar a mesma fonte/resultado; não criar SQL paralelo apenas para outro formato.
- Cálculos históricos devem respeitar a semântica da fonte de verdade e o corte temporal definido pelo módulo.
- Filtros alterados invalidam exportação/prévia anterior até que o resultado correspondente seja atualizado.
- Formatação de exportação não altera semântica dos dados.

## 9. Testes e evidência

- Toda correção de bug relevante deve, quando tecnicamente viável, ganhar prova automatizada que falhe para o comportamento incorreto e proteja o contrato corrigido.
- Testes devem isolar seus próprios dados e não depender acidentalmente de fixtures fora do cenário.
- CI verde de commit anterior não valida um HEAD posterior. A aprovação sempre referencia o SHA exato.
- Não fazer rerun cego de falha: primeiro identificar causa e distinguir falha de teste, fixture, infraestrutura e produção.
- Mudanças de segurança, concorrência, saldo/custo e integridade exigem atenção explícita na auditoria.

## 10. PONYTAIL e promoção

- Trabalho de risco ou mudança substancial deve ocorrer em branch isolada; produção permanece intocada até o gate de promoção.
- Gates acordados não são renumerados ou reduzidos durante a execução.
- Cada gate fecha somente com evidência correspondente; mudanças após um CI verde reabrem a necessidade de validar o novo HEAD.
- Antes da promoção: auditar diff completo contra a base, confirmar ausência de mudanças não relacionadas e CI verde do candidato exato.
- Promoção exige aprovação explícita do usuário.
- Merge deve ser travado ao HEAD aprovado quando a ferramenta permitir.
- Após merge, verificar o SHA efetivo de main e o production gate correspondente antes de declarar conclusão.
- Em falha de produção, parar e diagnosticar. Não fazer rerun, rollback, alteração de secret ou commit corretivo automático sem análise e autorização cabível.

## 11. Exceções

Qualquer exceção a estas invariantes deve ser deliberada: registrar qual regra está sendo excepcionada, motivo, impacto e evidência que justifica a decisão. Conveniência de implementação, por si só, não é justificativa.
