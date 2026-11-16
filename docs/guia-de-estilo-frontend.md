# Guia de estilo do front-end (contrato de layout)

Este guia vale para todas as telas, inclusive as páginas servidas pelo ESP32 da impressora (seção 8). Ele complementa o `global-engineering-contract.md`: não muda regra de negócio, só define como a interface se parece, se organiza e fala.

Quem mexer na interface deve seguir estas regras. Se uma regra atrapalhar, mude o guia no mesmo commit.

**Versão 2, de 16/11/2026.** As seções 2 a 5 e a 8 foram reescritas: paleta tirada da logo, página sem painel, tabela no lugar de cartões, avisos em texto, uma fonte em dois pesos, e as mesmas regras nas páginas do ESP32 da impressora. O código foi levado para esta versão no mesmo dia; a situação de cada item está na seção **Layout** do `plano-de-correcao.md`.

## 1. Princípios

1. **Uma tarefa por tela.** Cada tela responde a uma pergunta do operador (o que entrou? quanto tenho?). O que não ajuda a responder fica atrás de "Mais detalhes".
2. **Celular e computador são dois modos de trabalho. Abaixo de 900 px vale o modo celular; a tela de lançamento (Entrada, Saída, Transferir) só vira área de trabalho em duas colunas a partir de 1000 px, e as telas de cadastro usam duas colunas a partir de 1280 px, porque o menu lateral ocupa 232 px.** No celular, o operador está em pé, com uma mão, em passos curtos. No computador, está sentado, com leitor de código e teclado, e vê tudo de uma vez. O HTML é o mesmo; a organização de cada modo é pensada separadamente (ver `movement.css`).
3. **O servidor decide, a tela mostra.** Nenhuma regra de estoque, permissão ou saldo é calculada no navegador.
4. **Feedback ao lado de quem agiu.** A mensagem de erro ou sucesso aparece junto do botão que a causou.

## 2. Cores (função, não decoração)

A paleta vem da logo: preto, branco e o verde `#80BB04`. O resto é cinza neutro e duas cores de aviso. Use sempre as variáveis de `app.css`. Não crie cor nova sem registrar aqui.

| Função | Variável | Valor | Quando usar |
|---|---|---|---|
| Ação principal e página atual | `--brand-primary` | `#80BB04` | Fundo do botão principal e do item atual do menu. O texto por cima é preto |
| Ação principal sob o cursor / pressionada | `--brand-primary-hover`, `--brand-primary-active` | `#74AA04`, `#699A03` | Só nesses dois estados |
| Texto | `--text-primary` | `#000000` | Título, valor, rótulo de botão, link |
| Texto de apoio | `--text-secondary` | `#3D3D3D` | Frase de apoio, detalhe de linha |
| Dica e legenda | `--text-muted` | `#5C5C5C` | Rótulo de campo, cabeçalho de tabela, dica |
| Fundo | `--surface` | `#FFFFFF` | Página, campos, barras. `--background` passa a ter o mesmo valor |
| Fundo de destaque | `--surface-secondary` | `#F3F3F3` | Linha selecionada ou aberta, botão desativado, trecho de código |
| Linha | `--border` | `#DADADA` | Separação entre linhas e blocos |
| Contorno de controle | `--border-strong` | `#8A8A8A` | Borda de campo e de botão secundário |
| Falta e erro | `--danger` | `#B42318` | Estoque abaixo do mínimo, falha, ação destrutiva |
| Atenção | `--warning` | `#8A5A00` | Lançamento sem confirmação, divergência de contagem |
| Sucesso | `--success` | `#1F6F43` | Texto de operação concluída |

Regras:
- Verde é fundo, nunca texto. Verde sobre branco não tem contraste para leitura (2,3:1); preto sobre verde tem (9:1).
- Um verde só. As variáveis antigas (`-soft` e `-border` de sucesso, erro e informação, `--info`, `--accent`, `--focus-ring`, `--radius-md`, `--radius-lg` e as sombras) continuam declaradas só como apelidos dos valores desta tabela, para as regras antigas não quebrarem. Não use em código novo.
- Vermelho, âmbar e verde-escuro de sucesso aparecem só como cor de texto ou de número, nunca como fundo de bloco nem como faixa lateral.
- Nunca use cor sozinha para dar significado: acompanhe de texto ("Faltam 4 kg").
- O foco do teclado é um contorno preto de 3 px, afastado 2 px.
- Entrada e saída usam as mesmas cores. Mudam o título, o sinal (+ / −) e o texto do botão.
- Sem degradê, em nenhuma tela.

## 3. Espaço, forma e tipografia

- **A página é a superfície.** Não existe painel em volta do conteúdo da tela: `.panel` não tem borda, fundo, raio nem sombra. O conteúdo fica direto sobre o branco, separado por espaço e por linhas.
- **Moldura só para objeto.** Borda em volta de um bloco é permitida quando o bloco representa uma coisa que existe fora da tela ou que flutua sobre ela: a caixa de hortifruti, a etiqueta em pré-visualização, a folha de formulário do celular, a câmera, uma confirmação. Lista, grupo de campos, filtro e resumo não levam moldura. Nunca moldura dentro de moldura.
- **Sem faixa lateral colorida** e sem bolinha, selo ou pílula de situação. A situação é escrita na linha.
- **Escala de espaço:** 4, 8, 16, 24, 32 px. Entre campos do mesmo grupo, 8 ou 12; entre grupos, 16 ou 24.
- **Proximidade (Gestalt):** o que é do mesmo assunto fica junto. A distância entre grupos é sempre maior que a distância dentro do grupo. É o espaço que agrupa, não a moldura.
- **Cantos:** um raio só, `--radius-sm` (4 px), em campos e botões. Tabelas, linhas, barras, diálogos e blocos têm canto reto.
- **Sombra:** nenhuma. A barra fixa do celular se separa do conteúdo por uma linha de 1 px. O que flutua sobre a página (menu da conta, menu "Mais", diálogo) tem contorno preto de 1 px.
- **Tipografia:** Barlow, em dois pesos: 400 (texto) e 600 (título, valor, botão, item atual). Os pesos 500, 700 e 800 não são usados. A fonte é servida pelo próprio sistema, em `/static/fonts` (arquivos WOFF2 e a licença OFL), porque a política de segurança da página (`default-src 'self'`) não aceita fonte de outro endereço. Sem a fonte, vale a fonte do sistema.
- **Tamanhos:** título de tela 1.75rem (2rem a partir de 900 px); título de bloco 1.125rem; corpo 16 px; legenda, rótulo de campo e cabeçalho de tabela 14 px. Nada abaixo de 14 px, com uma exceção: o rótulo sob o ícone na barra inferior do celular (12 px), enquanto a barra tiver até oito itens (ver a decisão do menu por perfil no plano de correção). Quantidades que o operador digita usam 1.25rem a 1.5rem.
- **Números:** alinhados à direita, com `font-variant-numeric: tabular-nums`, sempre com a unidade.
- **Sem caixa alta com espaçamento** em rótulos. Fonte monoespaçada só para código de verdade (ZPL, variável de modelo), nunca para rótulo ou número.
- **Alvo de toque:** 44 px de altura no mínimo; 52 px nos botões da barra fixa do celular.

Onde este guia ainda diz "cartão" nas descrições de lançamento e de cadastro, leia "bloco de linha": o conteúdo de um item, separado do seguinte por uma linha, sem moldura.

## 4. Componentes

**Botões — só três tipos.**
1. Principal (fundo verde, texto preto em peso 600): concluir a tarefa. No máximo um por tela ou por passo.
2. Secundário (fundo branco, contorno `--border-strong`, texto preto): voltar, cancelar, ações de apoio.
3. Texto (preto, sublinhado): ações discretas como "Sair", "Alterar senha" e "Ver estoque completo".

Ação destrutiva usa o secundário com texto e contorno em `--danger` e pede confirmação. Botão desativado: fundo `--surface-secondary`, texto `--text-muted`. Não acrescente seta ao texto de botão ou link.

Botão de alternância (filtro da contagem, atalho de período dos relatórios): desligado é o secundário; ligado tem fundo preto e texto branco. O verde fica para a ação principal e a página atual.

**Campos.** Rótulo sempre visível acima do campo (nunca só placeholder), em 14 px e `--text-muted`. Dica em 14 px abaixo. Erro abaixo do campo ou na linha de feedback do formulário. Borda `--border-strong`, raio de 4 px, 48 px de altura.

**Tabelas (a partir de 900 px).** Dado em colunas é tabela, sem moldura em volta:
- cabeçalho em 14 px, peso 400, `--text-muted`, com uma linha preta de 2 px embaixo;
- linhas de 48 px no mínimo, separadas por `--border`;
- texto à esquerda, número à direita;
- a linha aberta ou selecionada recebe fundo `--surface-secondary` e nome em peso 600; o detalhe abre logo abaixo, no mesmo fundo (no Estoque: uma linha por marca, que abre as embalagens com "Dar saída" e "Dar entrada");
- ações da linha ficam na última coluna.

**Listas (abaixo de 900 px).** A tabela vira lista de linhas, não de cartões: nome em cima, detalhe em 14 px `--text-muted` embaixo, valor à direita em 1.125rem e peso 600. Uma linha preta de 2 px abre a lista; `--border` separa os itens.

**Situação.** Escreva o que falta, não um rótulo genérico. No Estoque, o item abaixo do mínimo mostra a quantidade e "Faltam 4 kg" em `--danger` e peso 600; o item normal não mostra nada na coluna Situação; o item sem mínimo cadastrado mostra "sem mínimo" na coluna Mínimo. A quantidade que falta vem do servidor (princípio 3), nunca de conta feita no navegador.

**Avisos.** Texto, não caixa. A mensagem (`.operation-feedback`) aparece ao lado de quem agiu, em uma linha, sem fundo nem borda: neutro e carregando em `--text-muted`; sucesso em `--success`; erro em `--danger` e peso 600. Frase curta, dizendo o que aconteceu e o que fazer. A única exceção é o aviso que trava ou muda o resultado de uma operação (`.impact-warning`: lançamento sem confirmação, impacto de uma alteração de cadastro): ele leva contorno preto de 1 px e texto em `--warning`, com os botões da ação ("Conferir", "Enviar de novo").

**Abas.** Texto com sublinhado preto de 2 px na aba atual, sobre uma linha `--border`. Sem fundo, sem sombra, sem seletor em formato de pílula.

**Cadastros no celular (Catálogo e Produtos).** Abaixo de 900 px o Catálogo mostra uma lista por vez, com abas Itens e Marcas; a próxima página carrega quando o fim da lista aparece. O formulário abre como folha presa à base da tela, com as ações fixas embaixo. O aviso de validação fica dentro do formulário; o de sucesso, acima da lista.

**Lançamento em lote (desktop, a partir de 1000 px).** Entrada, Saída e Transferência usam a tela de lote do mockup "Operar em lote": seletor de tipo no alto, depósito(s), campo de leitura em destaque, tabela de produtos (estoque atual, quantidade, preço da embalagem na entrada, estoque após) e resumo fixo à direita com o botão principal. O servidor grava o lote inteiro ou nada (`/api/movements/{entry,exit,transfer}/batch`). No celular, a **Saída** também usa o lote (desde 15/11/2026): cada produto vira um cartão com botões − e + (quantidade em embalagens), o botão **Câmera** lê os códigos em tela cheia e ler de novo o mesmo produto soma 1; o aviso da leitura aparece junto da busca, e a confirmação fica numa barra fixa acima do menu inferior, com o aviso logo acima dos botões. A busca não recebe foco sozinha no celular, para o teclado não cobrir a lista. Entrada e Transferência no celular continuam no assistente de 3 passos, um produto por vez. A câmera (`#scanner`) fica no passo 1 do assistente; no lote do celular ela é levada para o cartão de busca, para o aviso de câmera negada aparecer junto do botão. Lançamento sem confirmação trava também a câmera e os cartões, e o aviso com “Conferir” é trazido para a tela.

## 5. Navegação

- **Cabeçalho:** logo e nome do sistema ("Gestão de Estoque") à esquerda, conta à direita, uma linha `--border` embaixo. O slogan aparece só na tela de entrada.
- **Computador (≥ 900 px):** menu lateral fixo, na esquerda, separado do conteúdo por uma linha, com grupos: *Dia a dia* (Início, Estoque, Entrada, Caixas, Saída, Requisições, Transferir, Minha rota), *Cadastros* (Catálogo, Produtos, Patrimônio, Fichas técnicas, Locais e frota) e *Controle* (Cardápio, Expedição, Contagem de estoque, Relatórios, Administração). O nome do grupo fica em 14 px, `--text-muted`, sem caixa alta.
- **Celular (< 900 px):** barra superior com marca e conta; barra inferior com as ações do dia a dia e "Mais" para o resto.
- O item da página atual (`aria-current="page"`) tem fundo `--brand-primary` e texto preto em peso 600, no menu lateral e na barra inferior. Os demais itens são texto preto sobre branco; sob o cursor, fundo `--surface-secondary`.
- Quem não tem permissão não vê o item. A permissão real continua no servidor.

**Início e Estoque.** Início (`/home`) é a página de entrada: atalhos de lançamento e tabela de itens abaixo do mínimo, vinda de `/api/stock`. No desktop, Estoque é uma tabela (Item, Estoque, Mínimo, Situação); no celular, lista de linhas. As duas telas usam a mesma tabela e a mesma regra de situação da seção 4.

**Entrada no sistema.** Logo, título e campos direto sobre o branco, em uma coluna de até 380 px. Sem cartão, sem sombra, sem fundo decorado.

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
8. **Quando não se sabe se algo foi gravado, diga isso com clareza.** Use "sem confirmação" e ofereça "Conferir" e "Enviar de novo". Nunca "pendente" nem "pendência local". Nos cadastros (item, marca, produto), "Enviar de novo" já confere: o servidor devolve o cadastro original se ele já estiver gravado. Por isso o aviso oferece "Enviar de novo" e "Descartar".
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
| Cadastro pendente (item, marca, produto) | Cadastro sem confirmação |
| Conflito de identificação da operação | Este lançamento já foi enviado com outros dados |
| Reenviar operação | Enviar de novo |
| Verificar resultado | Conferir |
| Warehouse | Depósito |
| Inventário cego / total (na tela) | Contagem às cegas / completa |
| Unidade patrimonial, plaqueta/tag | Patrimônio, plaqueta |
| Receita, BOM, recipe | Ficha técnica (lista de itens do estoque de um prato) |
| Yield, rendimento | Porções que a receita rende |
| Food cost, margem de contribuição | CMV por refeição (custo das porções ÷ faturamento da refeição do grupo) |
| Customer, comprador | Cliente (quem compra o prato; o preço vem do grupo de clientes e da refeição) |
| Custo médio, PMP | Custo unitário (preço médio do estoque) |
| Menu planner, planejamento | Plano (de cardápio) |
| Headcount, refeições estimadas | Refeições previstas (premissa do plano) |
| Refeições servidas, realizado | Quantidade real |
| Per capita cru | Per capita pronto (do contrato do grupo) |
| Cluster, carteira | Grupo de clientes |
| Slot, componente | Função no prato (proteína, base, feijão…) |
| Lock, fixar | Travar |
| Sugestão automática, otimização | Sugestão do solver |
| Pedido de compra, requisição | Solicitação de compra |
| Budget x actual | Previsto x realizado |
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

## 8. Páginas do ESP32 da impressora

As páginas servidas pelo firmware (`firmware/etiqueta_zd220/web_pages.h`: imprimir etiqueta, fila TotalFood, configuração, modelo) seguem este guia inteiro. O operador passa do sistema para a página do ESP e não deve perceber a troca.

- **Mesmas variáveis, mesmos valores.** O bloco `:root` do firmware é cópia do bloco de `app.css` da seção 2, com os mesmos nomes. Quem mudar uma cor ou o raio muda nos dois arquivos no mesmo PR.
- **Fonte.** O firmware não embute a Barlow: o ESP fica numa rede interna fechada e a memória é curta. Ele declara a mesma família e cai na fonte do sistema (`Barlow, system-ui, sans-serif`). Pesos, tamanhos e números tabulares são os mesmos da seção 3.
- **Sem painel.** `.panel` do firmware não tem borda, fundo nem raio; os blocos se separam por espaço e por linha.
- **Situação da impressora** (pronta, sem papel, pausada, sem conexão) é texto em peso 600 na cor da função, não pílula com bolinha.
- **Abas.** O seletor de tipo de etiqueta e as abas de página usam a aba da seção 4, com sublinhado preto: 2 px no seletor de tipo, 3 px nas abas de página.
- **Código.** O modelo ZPL e as variáveis de modelo continuam em fonte monoespaçada, sobre `--surface-secondary`, em texto preto.
- **Barra do navegador:** `theme-color` branco.
- **Textos:** seção 6 e glossário, sem exceção.
- **Fora do alcance:** a etiqueta impressa (ZPL) tem contrato próprio em `etiquetas-caixas-fila-manual.md` e não muda com este guia.
- Mudança visual nas páginas sobe a versão do firmware (`FW_VERSION`).

## 9. Antes de abrir um pull request de interface

- [ ] Abri a tela no celular (≈ 390 px) e no computador (≥ 1280 px).
- [ ] Só usei as variáveis da seção 2, o raio de 4 px e os pesos 400 e 600.
- [ ] Não há painel em volta da página, moldura dentro de moldura, sombra, degradê, faixa lateral colorida nem aviso em caixa colorida.
- [ ] Verde só aparece como fundo, com texto preto.
- [ ] Dado em colunas é tabela no computador e lista de linhas no celular; números à direita.
- [ ] Só há um botão principal por tela ou passo.
- [ ] Os textos seguem a seção 6 e o glossário.
- [ ] Se mexi em cor, raio ou componente, mexi também em `web_pages.h` (seção 8).
- [ ] Os testes de contrato (`go test ./cmd/server` e `firmware/tests`) passam; se mudei um texto ou estilo citado nos testes, atualizei os dois juntos.
