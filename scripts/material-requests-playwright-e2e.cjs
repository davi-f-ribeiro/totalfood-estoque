'use strict';
// Tela de Requisições no Chrome, a 390 px, com a API simulada: cada aviso
// precisa aparecer junto da ação que o gerou e dentro da tela (contrato
// global, seção 3), com mensagem que diz a ação e o objeto.
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const {chromium}=require('playwright');
const root=path.resolve(__dirname,'..');
const original=fs.readFileSync(path.join(root,'cmd/server/templates/material_requests.html'),'utf8');
const html=original.replace('{{template "header" .}}','<header>Gestão de Estoque</header>')
.replace('{{template "nav" .}}','<nav><a href="/material-requests">Requisições</a></nav>')
.replaceAll('{{.CSRF}}','e2e-token').replaceAll('{{.User.ID}}','7')
.replace(/\{\{if \.CanMaterial(Write|Fulfill|Receive)\}\}1\{\{else\}\}0\{\{end\}\}/g,'1')
.replaceAll('{{if .CanMaterialWrite}}','').replaceAll('{{end}}','');
const state={scope:'all',requests:[],created:new Map(),reservations:[],deliveries:[],deliverOps:new Map(),failDeliver:true,received:new Set(),crates:[],version:1,qty:'10',extra:[]};
const respond=(route,status,body)=>route.fulfill({status,contentType:'application/json',body:JSON.stringify(body)});
const request1=()=>({id:1,unit_name:'Cozinha Central',requested_by:'Ana',production_date:'2026-11-16',service:'lanche',delivery_date:'2026-11-16',
 status:state.deliveries.length?'ENTREGA_PARCIAL':'PENDENTE',created_at:'2026-11-15T10:00:00Z',notes:'',version:state.version,
 lines:[{id:1,item_id:20,item:'Feijão carioca',unit:'KG',quantity:state.qty,reserved:String(state.reservations.reduce((s,r)=>s+Number(r.remaining),0)),delivered:String(state.deliveries.reduce((s,d)=>s+Number(d.quantity),0))},...state.extra],
 reservations:state.reservations,
 deliveries:state.deliveries.map(d=>({...d,received:state.received.has(d.id),received_by:state.received.has(d.id)?'Ana':''})),
 events:[]});

async function main(){
 const localServer=http.createServer((req,res)=>{
  const u=new URL(req.url,'http://127.0.0.1');
  const candidate=path.resolve(root,'cmd/server'+u.pathname);
  const staticRoot=path.resolve(root,'cmd/server/static');
  if(!u.pathname.startsWith('/static/')||!candidate.startsWith(staticRoot+path.sep)||!fs.existsSync(candidate)){res.writeHead(404);res.end();return}
  res.writeHead(200,{'Content-Type':candidate.endsWith('.js')?'application/javascript':candidate.endsWith('.css')?'text/css':'application/octet-stream'});
  fs.createReadStream(candidate).pipe(res);
 });
 await new Promise(resolve=>localServer.listen(0,'127.0.0.1',resolve));
 const base='http://127.0.0.1:'+localServer.address().port;
 const b=await chromium.launch({headless:true,executablePath:process.env.CHROME_BIN,args:['--no-sandbox']});
 try{
  const page=await b.newPage({viewport:{width:390,height:844},deviceScaleFactor:1});
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  page.on('dialog',d=>d.accept());
  await page.route('**/material-requests',r=>r.request().isNavigationRequest()
   ?r.fulfill({status:200,contentType:'text/html; charset=utf-8',body:html}):r.continue());
  await page.route('**/api/**',async route=>{
   const req=route.request(),u=new URL(req.url()),p=u.pathname;
   let body={};try{body=req.postDataJSON()||{}}catch{}
   state.requests.push({p,method:req.method(),body,params:Object.fromEntries(u.searchParams)});
   if(p==='/api/material-requests/search'){
    const rows=[
     {id:1,unit_name:'Cozinha Central',production_date:'2026-11-16',delivery_date:'2026-11-15',service:'lanche',items:1,status:request1().status},
     {id:2,unit_name:'Nutrição',production_date:'2026-11-17',delivery_date:'2026-11-16',service:'cafe',items:2,status:'PENDENTE'}
    ];
    const own=state.scope==='own';
    const visible=own?rows.slice(0,1):rows;
    const filtered=visible.filter(r=>['unit','status','service'].every(k=>!u.searchParams.get(k)||r[k==='unit'?'unit_name':k]===u.searchParams.get(k)))
     .filter(r=>!u.searchParams.get('q')||u.searchParams.get('q')==='#'+r.id);
    const index=Number(u.searchParams.get('page'))-1;
    return respond(route,200,{requests:filtered.slice(index,index+1),total:visible.length,filtered:filtered.length,
     page_size:1,units:visible.map(r=>r.unit_name),scope:own?'own':'all'});
   }
   if(p==='/api/material-requests'&&req.method()==='POST'){
    if(!state.created.has(body.operation_id))state.created.set(body.operation_id,2);
    return respond(route,201,{id:state.created.get(body.operation_id),status:'confirmed'});
   }
   if(p==='/api/material-requests/items')return respond(route,200,[{id:10,name:'Arroz branco',unit:'KG'}]);
   if(p==='/api/material-requests/1')return respond(route,200,request1());
   if(p==='/api/material-requests/options/20')return respond(route,200,[{sku_id:5,warehouse_id:1,brand:'Kicaldo',sku:'Feijão 1 kg',warehouse:'Câmara Fria',available:'40',stock:'40'}]);
   if(p==='/api/material-requests/1/reserve'){
    state.reservations.push({id:30,line_id:1,brand:'Kicaldo',sku:'Feijão 1 kg',warehouse:'Câmara Fria',quantity:body.quantity,remaining:body.quantity,status:'ATIVA'});
    return respond(route,201,{id:30,status:'confirmed'});
   }
   if(p==='/api/material-requests/1/deliver'){
    if(!state.deliverOps.has(body.operation_id)){
     const n=state.deliveries.length;state.deliverOps.set(body.operation_id,{id:40+n,movement_id:900+n});
     const r=state.reservations.find(x=>x.id===body.reservation_id);r.remaining=String(Number(r.remaining)-Number(body.quantity));if(Number(r.remaining)<=0)r.status='CONSUMIDA';
     const made=state.deliverOps.get(body.operation_id);
     state.deliveries.push({id:made.id,reservation_id:body.reservation_id,quantity:body.quantity,movement_id:made.movement_id,delivered_at:'2026-11-15T11:00:00Z',delivered_by:'Bruno',crates:(body.crate_ids||[]).map(id=>({id,code:'CX-00000'+id,quantity:state.crates.find(c=>c.id===id).weight}))});
     if(body.crate_ids)state.crates=state.crates.filter(c=>!body.crate_ids.includes(c.id));
    }
    if(state.failDeliver){state.failDeliver=false;return respond(route,503,{error:'Resposta perdida após gravar'})}
    return respond(route,201,{...state.deliverOps.get(body.operation_id),status:'confirmed'});
   }
   if(p==='/api/material-requests/1/deliveries/40/receive'){state.received.add(40);return respond(route,200,{id:40,status:'confirmed'})}
   if(p==='/api/crates')return respond(route,200,state.crates);
   if(p==='/api/material-requests/1/edit'){
    state.version++;state.qty=body.lines.find(l=>l.item_id===20).quantity;
    state.extra=body.lines.filter(l=>l.item_id!==20).map((l,i)=>({id:50+i,item_id:l.item_id,item:'Arroz branco',unit:'KG',quantity:l.quantity,reserved:'0',delivered:'0'}));
    return respond(route,200,{id:1,status:'ENTREGA_PARCIAL',version:state.version,changed:true,changes:['Feijão carioca: 10 → 12 KG','incluído Arroz branco (3 KG)']});
   }
   return respond(route,404,{error:'Unknown route '+p});
  });
  const inViewport=sel=>page.locator(sel).first().evaluate(el=>{const r=el.getBoundingClientRect();return r.top>=-1&&r.bottom<=innerHeight+1});
  const topHidden=async()=>assert.equal(await page.locator('#materialStatus').isVisible(),false,'Aviso do topo não pode ser usado pelas ações');
  await page.goto(base+'/material-requests',{waitUntil:'networkidle'});

  await page.locator('#materialListCount',{hasText:'Exibindo 1 de 2 no resultado'}).waitFor();
  await page.locator('#materialListScope',{hasText:'todas as unidades'}).waitFor();
  await page.getByRole('button',{name:'Próxima',exact:true}).click();
  await page.locator('#materialPageLabel',{hasText:'Página 2 de 2'}).waitFor();
  await page.locator('#materialList button',{hasText:'#2 · Nutrição'}).waitFor();
  await page.getByRole('button',{name:'Anterior',exact:true}).click();
  await page.locator('#materialList button',{hasText:'#1 · Cozinha Central'}).waitFor();

  await page.getByText('Filtros e ordenação',{exact:true}).click();
  const filters=page.locator('#materialFilters');
  await filters.locator('[name=unit]').selectOption('Nutrição');
  await filters.locator('[name=status]').selectOption('PENDENTE');
  await filters.locator('[name=service]').selectOption('cafe');
  await filters.locator('[name=date_field]').selectOption('delivery');
  await filters.locator('[name=from]').fill('2026-11-16');
  await filters.locator('[name=to]').fill('2026-11-16');
  await filters.locator('[name=sort]').selectOption('oldest');
  await page.locator('#materialListOrder',{hasText:'Mais antigas primeiro'}).waitFor();
  await page.locator('#materialActiveFilters',{hasText:'Entrega desejada de 16/11/2026 até 16/11/2026'}).waitFor();
  await page.locator('#materialListCount',{hasText:'Exibindo 1 de 1 no resultado · 2 acessíveis · 1 fora dos filtros'}).waitFor();
  await page.locator('#materialList button',{hasText:'#2 · Nutrição'}).waitFor();
  const combined=state.requests.find(r=>r.params?.unit==='Nutrição'&&r.params.status==='PENDENTE'&&r.params.service==='cafe'&&r.params.sort==='oldest');
  assert.equal(combined.params.date_field,'delivery');
  assert.equal(combined.params.from,'2026-11-16');
  assert.equal(combined.params.to,'2026-11-16');
  assert.equal(combined.params.page,'1');

  await filters.locator('[name=q]').fill('#555');
  await page.getByText('Nenhuma requisição corresponde aos filtros.').waitFor();
  await page.getByRole('button',{name:'Limpar filtros'}).click();
  await page.locator('#materialListCount',{hasText:'Exibindo 1 de 2 no resultado'}).waitFor();
  assert.equal(await page.locator('#materialActiveFilters').isVisible(),false);
  assert.deepEqual(await filters.locator('[name=q]').inputValue(),'');
  state.scope='own';
  await filters.locator('[name=q]').fill('#1');
  await page.locator('#materialListScope',{hasText:'requisições criadas por você'}).waitFor();
  assert.deepEqual(await filters.locator('[name=unit] option').allTextContents(),['Todas as unidades','Cozinha Central']);
  state.scope='all';
  await page.getByRole('button',{name:'Limpar filtros'}).click();
  await page.locator('#materialListCount',{hasText:'Exibindo 1 de 2 no resultado'}).waitFor();
  const filterSize=await page.evaluate(()=>[document.documentElement.scrollWidth,innerWidth]);
  assert(filterSize[0]<=filterSize[1],'Filtros abertos sem rolagem lateral a 390 px '+filterSize);
  await page.getByText('Filtros e ordenação',{exact:true}).click();

  // Criar: erro e sucesso abaixo do botão, com o material e a unidade.
  await page.locator('#tabCreate').click();
  await page.locator('#sectionCreate').waitFor({state:'visible'});
  await page.locator('#materialSearch').fill('arroz');
  await page.locator('#materialMatches button').first().click();
  assert.equal(await page.evaluate(()=>document.activeElement===document.querySelector('#materialDraft input')),true,'Depois de escolher o material, o foco vai para a quantidade');
  await page.locator('#materialUnit').fill('Cozinha Central');
  await page.locator('#materialService').selectOption({label:'Lanche'});
  await page.locator('#materialCreateSubmit').click();
  await page.locator('#materialCreateStatus',{hasText:'Digite a quantidade de Arroz branco'}).waitFor();
  assert(await inViewport('#materialCreateStatus'),'Erro da criação precisa aparecer na tela');
  assert.equal(await page.locator('#materialDraft input').getAttribute('aria-invalid'),'true','Quantidade com erro fica marcada');
  await page.locator('#materialDraft input').fill('5');
  await page.locator('#materialCreateSubmit').click();
  await page.locator('#materialCreateStatus',{hasText:'Requisição #2 enviada ao estoque: 1 material para Cozinha Central.'}).waitFor();
  assert(await inViewport('#materialCreateStatus'),'Sucesso da criação precisa aparecer na tela');
  assert.equal(await page.locator('#materialUnit').inputValue(),'Cozinha Central','A unidade usada fica preenchida para a próxima requisição');
  assert.equal(state.requests.find(r=>r.p==='/api/material-requests'&&r.method==='POST').body.service,'lanche');
  await topHidden();
  await page.locator('#tabList').click();
  assert.equal(await page.locator('#sectionCreate').isVisible(),false,'Nova requisição some quando a aba Requisições está aberta');

  // Separar: aviso no cartão do material.
  await page.locator('#materialList button',{hasText:'#1 · Cozinha Central'}).click();
  await page.locator('#materialDetail').waitFor({state:'visible'});
  await page.locator('#materialDetail').getByText('Lanche',{exact:true}).waitFor();
  await page.locator('[data-note-key="line:1"] button',{hasText:'Separar material'}).click();
  await page.locator('[data-note-key="line:1"] select').waitFor();
  await page.locator('[data-note-key="line:1"] input').fill('4');
  await page.locator('[data-note-key="line:1"] button',{hasText:'Confirmar separação'}).click();
  await page.locator('[data-note-key="line:1"] [data-detail-note]',{hasText:'4 KG de Feijão carioca separados (Kicaldo, Câmara Fria).'}).waitFor();
  assert(await inViewport('[data-note-key="line:1"] [data-detail-note]'),'Aviso da separação precisa aparecer na tela');
  await topHidden();

  // Entregar: resposta perdida mostra "Enviar de novo" no próprio cartão;
  // o reenvio usa o mesmo operation_id e o sucesso aparece nas entregas.
  await page.locator('[data-note-key="res:30"] input').fill('4');
  await page.locator('[data-note-key="res:30"] button',{hasText:'Confirmar entrega'}).click();
  const uncertain=page.locator('[data-note-key="res:30"] [data-detail-note]',{hasText:'Sem confirmação do servidor para a entrega de 4 KG de Feijão carioca.'});
  await uncertain.waitFor();
  assert(await inViewport('[data-note-key="res:30"] [data-detail-note]'),'Aviso de entrega sem confirmação precisa aparecer na tela');
  await uncertain.locator('button',{hasText:'Enviar de novo'}).click();
  await page.locator('[data-note-key="deliveries"] [data-detail-note]',{hasText:'Entrega de 4 KG de Feijão carioca registrada (saída #900).'}).waitFor();
  assert(await inViewport('[data-note-key="deliveries"] [data-detail-note]'),'Sucesso da entrega precisa aparecer na tela');
  const delivers=state.requests.filter(r=>r.p==='/api/material-requests/1/deliver');
  assert.equal(delivers.length,2);
  assert.equal(delivers[0].body.operation_id,delivers[1].body.operation_id,'Reenvio precisa usar o mesmo operation_id');
  assert.equal(state.deliveries.length,1,'Reenvio não pode duplicar a entrega');
  await topHidden();

  // A entrega diz o que é: material, quantidade com unidade, marca e depósito.
  await page.locator('[data-note-key="delivery:40"] strong',{hasText:'Feijão carioca, 4 KG'}).waitFor();
  await page.locator('[data-note-key="delivery:40"]',{hasText:'Kicaldo, Feijão 1 kg, Câmara Fria'}).waitFor();

  // Receber: o motivo só aparece quando a quantidade recebida é menor que a entregue.
  const motive=page.locator('[data-note-key="delivery:40"] label',{hasText:'Motivo da diferença'});
  assert.equal(await motive.isVisible(),false,'Motivo escondido no recebimento integral');
  await page.locator('[data-note-key="delivery:40"] input').first().fill('3');
  await motive.waitFor();
  await page.locator('[data-note-key="delivery:40"] input').first().fill('4');
  await motive.waitFor({state:'hidden'});
  // Receber: aviso no cartão da entrega.
  await page.locator('[data-note-key="delivery:40"] button',{hasText:'Confirmar recebimento'}).click();
  await page.locator('[data-note-key="delivery:40"] [data-detail-note]',{hasText:'Recebimento da entrega #40 confirmado.'}).waitFor();
  assert(await inViewport('[data-note-key="delivery:40"] [data-detail-note]'),'Aviso do recebimento precisa aparecer na tela');
  await topHidden();

  // Item controlado por caixa: o estoquista marca as caixas, o peso é somado e a entrega
  // leva a lista de caixas. Sem caixa marcada, o aviso é definitivo, sem "Enviar de novo".
  state.reservations.push({id:31,line_id:1,sku_id:5,warehouse_id:1,brand:'Sem marca',sku:'Melão Japonês',warehouse:'Câmara Fria',quantity:'5',remaining:'5',status:'ATIVA'});
  state.crates=[{id:7,code:'CX-000007',sku_id:5,weight:'2.000'},{id:8,code:'CX-000008',sku_id:5,weight:'3.000'},{id:9,code:'CX-000009',sku_id:6,weight:'9.000'}];
  await page.locator('#materialList button',{hasText:'#1 · Cozinha Central'}).click();
  const crateCard=page.locator('[data-note-key="res:31"]');
  await crateCard.locator('input[type=checkbox]').first().waitFor();
  assert.equal(await crateCard.locator('input[type=checkbox]').count(),2,'Só as caixas do produto separado aparecem');
  await crateCard.locator('button',{hasText:'Confirmar entrega'}).click();
  await crateCard.locator('[data-detail-note]',{hasText:'Este produto é controlado por caixas. Marque as caixas que vão sair.'}).waitFor();
  assert.equal(await crateCard.locator('[data-detail-note] button').count(),0,'Recusa definitiva não oferece reenvio');
  await crateCard.locator('input[type=checkbox]').nth(0).check();
  await crateCard.locator('input[type=checkbox]').nth(1).check();
  assert.equal(await crateCard.locator('input:not([type=checkbox])').inputValue(),'5','A quantidade é a soma dos pesos das caixas');
  await crateCard.locator('button',{hasText:'Confirmar entrega'}).click();
  await page.locator('[data-note-key="deliveries"] [data-detail-note]',{hasText:'Entrega de 5 KG de Feijão carioca registrada (saída #901).'}).waitFor();
  const crateDeliver=state.requests.filter(r=>r.p==='/api/material-requests/1/deliver').pop();
  assert.deepEqual(crateDeliver.body.crate_ids,[7,8],'A entrega precisa levar as caixas marcadas');
  await page.locator('[data-note-key="delivery:41"]',{hasText:'Caixas: CX-000007 (2 kg), CX-000008 (3 kg)'}).waitFor();

  // Editar: item já entregue não sai do pedido; inclui outro material e corrige a quantidade.
  await page.locator('[data-note-key="request"] button',{hasText:'Editar requisição'}).click();
  const editBox=page.locator('[data-note-key="edit"]');
  await editBox.waitFor();
  assert.equal(await editBox.locator('.material-line button',{hasText:'Remover'}).first().isDisabled(),true,'Item com entrega não pode ser removido');
  assert.equal(await editBox.locator('input[type=date]').nth(1).isDisabled(),true,'Data de produção fica travada depois da entrega');
  await editBox.locator('input[type=search]').fill('arroz');
  await editBox.locator('.material-matches button').first().click();
  await editBox.locator('button',{hasText:'Salvar alterações'}).click();
  await editBox.locator('[data-detail-note]',{hasText:'Digite a quantidade de Arroz branco'}).waitFor();
  assert(await inViewport('[data-note-key="edit"] [data-detail-note]'),'Erro da edição precisa aparecer na tela');
  await editBox.locator('.material-line input').nth(0).fill('12');
  await editBox.locator('.material-line input').nth(1).fill('3');
  await editBox.locator('button',{hasText:'Salvar alterações'}).click();
  await page.locator('[data-note-key="request"] [data-detail-note]',{hasText:'Requisição #1 alterada: Feijão carioca: 10 → 12 KG; incluído Arroz branco (3 KG).'}).waitFor();
  assert(await inViewport('[data-note-key="request"] [data-detail-note]'),'Sucesso da edição precisa aparecer na tela');
  assert.equal(await page.locator('[data-note-key="edit"]').count(),0,'Formulário fecha depois de salvar');
  const sentEdit=state.requests.find(r=>r.p==='/api/material-requests/1/edit').body;
  assert.equal(sentEdit.version,1);
  assert.deepEqual(sentEdit.lines,[{item_id:20,quantity:'12'},{item_id:10,quantity:'3'}]);
  await page.locator('#materialDetail',{hasText:'Arroz branco'}).first().waitFor();
  await topHidden();

  assert.equal(await page.getByText('Operação registrada.').count(),0,'Mensagem genérica não pode voltar');
  const size=await page.evaluate(()=>[document.documentElement.scrollWidth,innerWidth]);
  assert(size[0]<=size[1],'Rolagem lateral a 390 px '+size);
  // Computador (1280 px): a lista fica à esquerda e o pedido aberto ao lado, sem rolagem lateral.
  await page.setViewportSize({width:1280,height:800});
  await page.locator('#materialList button',{hasText:'#1 · Cozinha Central'}).click();
  await page.locator('#materialDetail').waitFor({state:'visible'});
  const boxes=await page.evaluate(()=>{const b=id=>document.getElementById(id).getBoundingClientRect();return{list:b('materialRequests'),detail:b('materialDetail')}});
  assert(boxes.detail.left>=boxes.list.right,'No computador, o detalhe fica ao lado da lista '+JSON.stringify(boxes));
  assert(Math.abs(boxes.detail.top-boxes.list.top)<40,'Lista e detalhe começam na mesma altura');
  const desk=await page.evaluate(()=>[document.documentElement.scrollWidth,innerWidth]);
  assert(desk[0]<=desk[1],'Rolagem lateral a 1280 px '+desk);
  await page.screenshot({path:process.env.SHOT_DESKTOP||'/tmp/material-desktop.png',fullPage:false});
  assert.deepEqual(errors,[]);
  console.log(JSON.stringify({status:'PASS',requests:state.requests.length},null,1));
 }finally{await b.close();localServer.close()}
}
main().catch(e=>{console.error('E2E FAILED',e);process.exitCode=1});
