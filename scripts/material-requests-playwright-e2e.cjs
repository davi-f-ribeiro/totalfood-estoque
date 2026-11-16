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
const state={requests:[],created:new Map(),reservations:[],deliveries:[],deliverOps:new Map(),failDeliver:true,received:new Set()};
const respond=(route,status,body)=>route.fulfill({status,contentType:'application/json',body:JSON.stringify(body)});
const request1=()=>({id:1,unit_name:'Cozinha Central',requested_by:'Ana',production_date:'2026-11-16',service:'almoco',delivery_date:'2026-11-16',
 status:state.deliveries.length?'EM_ATENDIMENTO':'ABERTA',created_at:'2026-11-15T10:00:00Z',notes:'',
 lines:[{id:1,item_id:20,item:'Feijão carioca',unit:'KG',quantity:'10',reserved:String(state.reservations.reduce((s,r)=>s+Number(r.remaining),0)),delivered:String(state.deliveries.reduce((s,d)=>s+Number(d.quantity),0))}],
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
   state.requests.push({p,method:req.method(),body});
   if(p==='/api/material-requests'&&req.method()==='GET')return respond(route,200,[{id:1,unit_name:'Cozinha Central',production_date:'2026-11-16',service:'almoco',items:1,status:request1().status}]);
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
     state.deliverOps.set(body.operation_id,{id:40,movement_id:900});
     const r=state.reservations.find(x=>x.id===body.reservation_id);r.remaining=String(Number(r.remaining)-Number(body.quantity));if(Number(r.remaining)<=0)r.status='CONSUMIDA';
     state.deliveries.push({id:40,quantity:body.quantity,movement_id:900,delivered_at:'2026-11-15T11:00:00Z',delivered_by:'Bruno'});
    }
    if(state.failDeliver){state.failDeliver=false;return respond(route,503,{error:'Resposta perdida após gravar'})}
    return respond(route,201,{...state.deliverOps.get(body.operation_id),status:'confirmed'});
   }
   if(p==='/api/material-requests/1/deliveries/40/receive'){state.received.add(40);return respond(route,200,{id:40,status:'confirmed'})}
   if(p==='/api/crates')return respond(route,200,[]);
   return respond(route,404,{error:'Unknown route '+p});
  });
  const inViewport=sel=>page.locator(sel).first().evaluate(el=>{const r=el.getBoundingClientRect();return r.top>=-1&&r.bottom<=innerHeight+1});
  const topHidden=async()=>assert.equal(await page.locator('#materialStatus').isVisible(),false,'Aviso do topo não pode ser usado pelas ações');
  await page.goto(base+'/material-requests',{waitUntil:'networkidle'});

  // Criar: erro e sucesso abaixo do botão, com o material e a unidade.
  await page.locator('#materialSearch').fill('arroz');
  await page.locator('#materialMatches button').first().click();
  await page.locator('#materialUnit').fill('Cozinha Central');
  await page.locator('#materialCreateSubmit').click();
  await page.locator('#materialCreateStatus',{hasText:'Informe a quantidade de Arroz branco'}).waitFor();
  assert(await inViewport('#materialCreateStatus'),'Erro da criação precisa aparecer na tela');
  await page.locator('#materialDraft input').fill('5');
  await page.locator('#materialCreateSubmit').click();
  await page.locator('#materialCreateStatus',{hasText:'Requisição #2 enviada ao estoque: 1 material para Cozinha Central.'}).waitFor();
  assert(await inViewport('#materialCreateStatus'),'Sucesso da criação precisa aparecer na tela');
  await topHidden();

  // Separar: aviso no cartão do material.
  await page.locator('#materialList button',{hasText:'#1 · Cozinha Central'}).click();
  await page.locator('#materialDetail').waitFor({state:'visible'});
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

  // Receber: aviso no cartão da entrega.
  await page.locator('[data-note-key="delivery:40"] button',{hasText:'Confirmar recebimento'}).click();
  await page.locator('[data-note-key="delivery:40"] [data-detail-note]',{hasText:'Recebimento da entrega #40 confirmado.'}).waitFor();
  assert(await inViewport('[data-note-key="delivery:40"] [data-detail-note]'),'Aviso do recebimento precisa aparecer na tela');
  await topHidden();

  assert.equal(await page.getByText('Operação registrada.').count(),0,'Mensagem genérica não pode voltar');
  const size=await page.evaluate(()=>[document.documentElement.scrollWidth,innerWidth]);
  assert(size[0]<=size[1],'Rolagem lateral a 390 px '+size);
  assert.deepEqual(errors,[]);
  console.log(JSON.stringify({status:'PASS',requests:state.requests.length},null,1));
 }finally{await b.close();localServer.close()}
}
main().catch(e=>{console.error('E2E FAILED',e);process.exitCode=1});
