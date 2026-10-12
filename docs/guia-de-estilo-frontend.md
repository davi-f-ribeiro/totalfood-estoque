# Guia de estilo do front-end

Este guia vale para todas as telas. Ele complementa o `global-engineering-contract.md`: não muda regra de negócio, só define como a interface se parece, se organiza e fala.

Quem mexer na interface deve seguir estas regras. Se uma regra atrapalhar, mude o guia no mesmo commit.

## 1. Princípios

1. **Uma tarefa por tela.** Cada tela responde a uma pergunta do operador (o que entrou? quanto tenho?). O que não ajuda a responder fica atrás de "Mais detalhes".
2. **Celular e computador são dois modos de trabalho. Abaixo de 900 px vale o modo celular; a tela de lançamento (Entrada, Saída, Transferir) só vira área de trabalho em duas colunas a partir de 1000 px, e as telas de cadastro usam duas colunas a partir de 1280 px, porque o menu lateral ocupa 232 px.** No celular, o operador está em pé, com uma mão, em passos curtos. No computador, está sentado, com leitor de código e teclado, e vê tudo de uma vez. O HTML é o mesmo; a organização de cada modo é pensada separadamente (ver `movement.css`).
3. **O servidor decide, a tela mostra.** Nenhuma regra de estoque, permissão ou saldo é calculada no navegador.
4. **Feedback ao lado de quem agiu.** A mensagem de erro ou sucesso aparece junto do botão que a causou.

## 2. Cores (função, não decoração)

Use sempre as variáveis de `app.css`. Não crie cor nova sem registrar aqui.

| Função | Variável | Quando usar |
|---|---|---|
| Ação principal | `--brand-primary` (`#4b9f05`) | Um botão por tela: a ação que conclui a tarefa |
| Ação principal, pressionada ou destaque de texto | `--brand-primary-active` | Passo atual, link de destaque |
| Fundo suave da marca | `--brand-primary-soft` | Item selecionado, linha ativa |
| Texto | `--text-primary`, `--text-secondary`, `--text-muted` | Título e valor / apoio / dica e legenda |
| Superfícies | `--surface`, `--surface-secondary`, `--background` | Cartão / bloco interno / fundo da página |
| Linhas | `--border` | Toda separação. Uma cor só |
| Sucesso | `--success` e `--success-soft` | Operação concluída, saldo acima do mínimo |
| Atenção | `--warning` | Abaixo do mínimo, divergência, pendência |
| Erro | `--danger` e `--danger-soft` | Falha, ação destrutiva |
| Informação | `--info` e `--info-soft` | Carregando, orientação neutra |

Regras:
- Verde é a cor da ação e da marca, não de "tudo". Texto e bordas ficam neutros.
- Nunca use cor sozinha para dar significado: acompanhe de texto ("Abaixo do mínimo") ou ícone.
- Entrada e saída usam as mesmas cores. Mudam o título, o sinal (+ / −) e o texto do botão.

## 3. Espaço, forma e tipografia

- **Escala de espaço:** 4, 8, 16, 24, 32 px. Entre campos do mesmo grupo, 8 ou 12; entre grupos, 16 ou 24.
- **Proximidade (Gestalt):** o que é do mesmo assunto fica junto e dentro do mesmo cartão. A distância entre grupos é sempre maior que a distância dentro do grupo.
- **Cantos:** dois raios apenas. `--radius-md` (10 px) em cartões e painéis; `--radius-sm` (6 px) em campos, botões e avisos.
- **Sombra:** só `--shadow-sm`, em cartões. Sombra grande apenas na barra fixa do celular.
- **Tipografia:** fonte do sistema. Título de tela 1.4rem; título de bloco 1rem em negrito; corpo 16px; legenda 13px. Quantidades que o operador digita usam tamanho maior (1.25rem a 1.5rem).
- **Alvo de toque:** 44 px de altura no mínimo; 52 px nos botões da barra fixa do celular.

## 4. Componentes

**Botões — só três tipos.**
1. Principal (verde cheio): concluir a tarefa. No máximo um por tela ou por passo.
2. Secundário (contorno): voltar, cancelar, ações de apoio.
3. Texto (`linkish`): ações discretas como "Sair" e "Alterar senha".

Ação destrutiva usa o secundário em vermelho e pede confirmação.

**Campos.** Rótulo sempre visível acima do campo (nunca só placeholder). Dica em texto pequeno abaixo. Erro abaixo do campo ou na linha de feedback do formulário.

**Listas e tabelas.** Uma linha por item: nome em destaque, detalhe em cinza embaixo, valor à direita. Em telas estreitas a tabela vira lista de cartões. Selecionado = fundo `--brand-primary-soft`.

**Avisos.** Um bloco (`.operation-feedback`) com quatro estados: neutro, carregando, sucesso, erro. Frase curta, dizendo o que aconteceu e o que fazer.

## 5. Navegação

- **Computador (≥ 900 px):** menu lateral fixo, na esquerda, com grupos: *Dia a dia* (Estoque, Entrada, Saída, Transferir), *Cadastros* (Catálogo, Produtos, Patrimônio) e *Controle* (Contagem de estoque, Relatórios, Administração). A conta (usuário, trocar senha, sair) fica no rodapé do menu.
- **Celular (< 900 px):** barra superior com marca e conta; barra inferior com as quatro ações do dia a dia e "Mais" para o resto.
- O item da página atual aparece marcado (`aria-current="page"`).
- Quem não tem permissão não vê o item. A permissão real continua no servidor.

## 6. Como escrever os textos da tela

O texto da interface é o cartão de visita do sistema, e quem lê está trabalhando, com pressa. Escreva como um profissional atento explicaria a um colega: claro, direto e sem enfeite. Texto descuidado passa a mesma impressão que layout descuidado.

**Voz.** O sistema fala de forma impessoal e se dirige ao usuário no imperativo. Não usa "nós" nem "a gente", não pede desculpa e não faz gracinha.

**Regras**

1. **Frases curtas, voz ativa, português do dia a dia.** "Escolha um produto para continuar." e não "É necessária a seleção de um produto para prosseguir."
2. **Não diga duas vezes a mesma coisa.** Nada de "mesmo" junto de "de novo", nem a mesma palavra em duas frases seguidas se uma só resolve. Título de aba: "Estoque atual · Gestão de Estoque", e não "Estoque atual · Estoque".
3. **Campos:** "Digite" para o que se escreve, "Escolha" para o que está numa lista. Não alterne com "Informe" ou "Selecione".
4. **Botão é verbo curto e diz o resultado:** "Salvar", "Confirmar entrada", "Criar produto", "Conferir". Nunca "Efetuar registro".
5. **Erro diz o que houve e o que fazer, nessa ordem.** "Não foi possível salvar. Confira a conexão e tente de novo." Quando só o usuário pode resolver, diga como; quando é falha do sistema, diga só "Tente de novo".
6. **"Não foi possível", nunca "Não deu para".** Tom formal sem ser burocrático. Evite "realizar", "efetuar", "possui", "permanece", "informado", "o mesmo" (como em "a mesma operação").
7. **Sem termos técnicos ou em inglês.** O usuário não vê SKU, warehouse, JSON, operation_id, offset nem "erro interno" solto.
8. **Quando não se sabe se algo foi gravado, diga isso com clareza.** Use "sem confirmação" e ofereça "Conferir" e "Enviar de novo". Nunca "pendente" nem "pendência local".
9. **Sem ponto de exclamação**, sem maiúsculas corridas. Títulos e rótulos sem ponto final; frases com ponto final.
10. **Números e datas no padrão brasileiro:** `1.250,50`; `11/10/2026`.
11. **Título de tela: substantivo curto** ("Estoque", "Entrada"). Subtítulo: uma frase que diz para que serve a tela.
12. **Mensagens do servidor valem o mesmo que as do front.** Tudo que o servidor devolve e pode aparecer na tela segue este guia. Se mudar um texto no servidor, confira se o JavaScript e os testes dependem dele.

### Glossário

| Evitar | Usar |
|---|---|
| SKU, apresentação | produto; “embalagem” quando falar da caixa, pacote ou fardo |
| Item pai | Item (o nome geral, ex.: “Leite”) |
| Produto/Item | Produto (item + marca + embalagem + código de barras) |
| Pendência local, operação pendente, aguardando envio | Lançamento sem confirmação |
| Conflito de identificação da operação | Este lançamento já foi enviado com outros dados |
| Reenviar operação | Enviar de novo |
| Verificar resultado | Conferir |
| Warehouse | Depósito |
| Inventário cego / total (na tela) | Contagem às cegas / completa |
| Unidade patrimonial, plaqueta/tag | Patrimônio, plaqueta |
| Inventário cego | Contagem às cegas |
| Inventário total | Contagem completa |
| Inventário (a atividade de contar) | Contagem |
| Saldo, saldo operacional | Estoque, quantidade em estoque |
| Documento/referência | Nota ou documento |
| Selecione… (em mensagens) | Escolha… |
| Perfil de acesso | Perfil |

Se criar um termo novo, acrescente aqui.

## 7. Estados que toda tela precisa mostrar

- **Carregando:** texto curto ("Carregando estoque…"), sem bloquear o resto da tela.
- **Vazio:** diga por que está vazio e o que fazer ("Nenhum produto encontrado. Tente outro nome ou código.").
- **Erro:** conforme a seção 6, com como tentar de novo.
- **Sem conexão:** o lançamento fica aguardando envio e a tela mostra isso com clareza; nunca perder o que o operador digitou.

## 8. Antes de abrir um pull request de interface

- [ ] Abri a tela no celular (≈ 390 px) e no computador (≥ 1280 px).
- [ ] Só usei variáveis de cor e os dois raios.
- [ ] Só há um botão principal por tela ou passo.
- [ ] Os textos seguem a seção 6 e o glossário.
- [ ] Os testes de contrato (`go test ./cmd/server`) passam; se mudei um texto citado nos testes, atualizei os dois juntos.
