'use strict';
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const {chromium}=require('playwright');
const root=path.resolve(__dirname,'..');
const original=fs.readFileSync(path.join(root,'cmd/server/templates/crates.html'),'utf8');
const html=original.replace('{{template "header" .}}','<header>Gestão de Estoque</header>')
.replace('{{template "nav" .}}','<nav><a href="/crates">Caixas</a></nav>')
.replaceAll('{{.CSRF}}','e2e-token').replaceAll('{{if .CanTransfer}}','')
.replaceAll('{{if .CanWarehouseAdmin}}','').replaceAll('{{end}}','');
const state={crates:[],jobs:[],requests:[],receipts:new Map(),weighs:new Map(),transfers:new Map(),failReceive:true,printers:[]};
const respond=(route,status,body)=>route.fulfill({status,contentType:'application/json',body:JSON.stringify(body)});
async function main(){
 // Serve repository static files locally so the browser test is standalone and
 // never depends on production, Neon, the WMS binary or an existing HTTP server.
 const localServer=http.createServer((req,res)=>{
   const u=new URL(req.url,'http://127.0.0.1');
   if(!u.pathname.startsWith('/static/')){res.writeHead(404);res.end();return;}
   const candidate=path.resolve(root,'cmd/server'+u.pathname);
   const staticRoot=path.resolve(root,'cmd/server/static');
   if(!candidate.startsWith(staticRoot+path.sep)||!fs.existsSync(candidate)){res.writeHead(404);res.end();return;}
   const type=candidate.endsWith('.js')?'application/javascript':candidate.endsWith('.css')?'text/css':'application/octet-stream';
   res.writeHead(200,{'Content-Type':type});
   fs.createReadStream(candidate).pipe(res);
 });
 await new Promise(resolve=>localServer.listen(0,'127.0.0.1',resolve));
 const base='http://127.0.0.1:'+localServer.address().port;
 const b=await chromium.launch({headless:true,executablePath:process.env.CHROME_BIN,args:['--no-sandbox']});
 try{
  const page=await b.newPage({viewport:{width:390,height:844},deviceScaleFactor:1});
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  page.on('dialog',d=>d.accept());
  await page.route('**/crates*',r=>r.request().isNavigationRequest()
    ?r.fulfill({status:200,contentType:'text/html; charset=utf-8',body:html}):r.continue());
  await page.route('**/api/**',async route=>{
    const req=route.request(),pathname=new URL(req.url()).pathname;
    let body={};try{body=req.postDataJSON()||{}}catch{}
    state.requests.push({pathname,method:req.method(),body});
    if(pathname==='/api/warehouses')return respond(route,200,[{id:1,name:'VR'},{id:2,name:'Câmara Fria'}]);
    if(pathname==='/api/products/lookup')return respond(route,200,[{sku_id:123,item:'Tomate E2E',brand:'',barcode:'',unit:'KG',content_quantity:'1'}]);
    if(pathname==='/api/print/jobs')return respond(route,200,{jobs:state.jobs});
    if(pathname==='/api/crates/receive'){
      let result=state.receipts.get(body.operation_id);
      if(!result){
        const crates=body.weights.map((weight,i)=>({id:state.crates.length+i+1,code:'CX-'+String(state.crates.length+i+1).padStart(6,'0'),warehouse_id:body.warehouse_id,position:i+1,total:body.weights.length,weight,revision:1,product:'Tomate E2E',document:body.document_number,status:'ACTIVE'}));
        state.crates.push(...crates);
        result={movement_id:500,lot_id:10,crates};state.receipts.set(body.operation_id,result);
        crates.forEach(c=>state.jobs.push({id:state.jobs.length+1,crate_id:c.id,crate_code:c.code,revision:c.revision,status:'PENDING',product:c.product,position:c.position,total:c.total}));
      }
      if(state.failReceive){state.failReceive=false;return respond(route,503,{error:'Resposta perdida após gravação no banco'});}
      return respond(route,200,result);
    }
    const match=pathname.match(/^\/api\/crates\/(\d+)(?:\/(weigh|transfer))?$/);
    if(match){
      const c=state.crates.find(x=>x.id===Number(match[1]));
      if(!c)return respond(route,404,{error:'Não encontrado'});
      if(req.method()==='GET')return respond(route,200,c);
      if(match[2]==='weigh'){
        if(!state.weighs.has(body.operation_id)){
          c.weight=body.weight;c.revision++;state.weighs.set(body.operation_id,{...c});
          state.jobs.push({id:state.jobs.length+1,crate_id:c.id,crate_code:c.code,revision:c.revision,status:'PENDING',product:c.product,position:c.position,total:c.total});
        }
        return respond(route,200,state.weighs.get(body.operation_id));
      }
      if(match[2]==='transfer'){
        if(!state.transfers.has(body.operation_id)){c.warehouse_id=body.to_warehouse_id;state.transfers.set(body.operation_id,{...c})}
        return respond(route,200,state.transfers.get(body.operation_id));
      }
    }
    if(pathname==='/api/print/devices'){
      state.printers.push(body.name);
      return respond(route,201,{id:90,name:body.name,token:'local-review-token-a12345678901234567890123456789012345'});
    }
    return respond(route,404,{error:'Unknown route '+pathname});
  });
  await page.goto(base+'/crates',{waitUntil:'networkidle'});
  assert.equal(await page.locator('#crateWeights input').count(),1);
  assert.equal(await page.locator('#crateWarehouse option').count(),2);
  let size=await page.evaluate(()=>[document.documentElement.scrollWidth,innerWidth]);
  assert(size[0]<=size[1],'Mobile horizontal overflow at load '+size);
  await page.locator('#crateSearch').fill('Tomate');
  await page.locator('#crateProductLookup button').first().waitFor();
  await page.locator('#crateProductLookup button').first().click();
  assert.equal(await page.locator('#crateSku').inputValue(),'123');
  await page.locator('#crateCount').fill('2');
  await page.locator('#crateWeights input').nth(0).fill('20,000');
  await page.locator('#crateWeights input').nth(1).fill('18,500');
  await page.locator('#crateCost').fill('3,30');
  await page.locator('#crateDocument').fill('CEASA-1234');
  assert.match(await page.locator('#crateTotal').innerText(),/38,500 kg/);
  await page.locator('#crateReceiveSubmit').click();
  await page.getByText('Não foi possível confirmar a entrada.',{exact:false}).waitFor();
  assert.equal(state.receipts.size,1,'O servidor gravou apesar de resposta 503');
  assert.equal(state.crates.length,2,'Primeira requisição criou só duas caixas');
  await page.locator('#crateReceiveSubmit').click();
  await page.getByText('Entrada registrada e etiquetas adicionadas à fila.').waitFor();
  const rec=state.requests.filter(r=>r.pathname==='/api/crates/receive');
  assert.equal(rec.length,2);
  assert.equal(rec[0].body.operation_id,rec[1].body.operation_id,'Idempotent retry');
  assert.equal(state.crates.length,2,'Retry não pode duplicar caixas');
  assert.match(await page.locator('#crateReceiveResult').innerText(),/CX-000001/);
  // Scanner without camera access must not make the manual fallback unusable.
  await page.locator('#scanCrate').click();
  await page.waitForTimeout(350);
  assert.equal(await page.locator('#crateFind').isEnabled(),true);
  if(await page.locator('#scanner').isVisible())await page.locator('#scanCancel').click();
  await page.locator('#crateFind').fill('CX-000001');
  await page.locator('#crateFindForm button[type=submit]').click();
  await page.locator('#crateDetail').waitFor({state:'visible'});
  await page.locator('#crateWeighWeight').fill('12,000');
  await page.locator('#crateWeighSubmit').click();
  await page.getByText('Pesagem registrada. Nova etiqueta adicionada à fila.').waitFor();
  assert.equal(state.weighs.size,1);
  await page.locator('#crateTransferWarehouse').selectOption('2');
  await page.locator('#crateTransferSubmit').click();
  await page.getByText('Caixa transferida sem alterar sua identificação.').waitFor();
  assert.equal(state.crates[0].warehouse_id,2);
  await page.locator('#cratePrinterName').fill('Zebra cozinha');
  await page.locator('#cratePrinterForm button[type=submit]').click();
  await page.getByText('Impressora cadastrada. Configure o token no ESP32.').waitFor();
  assert.equal(state.printers.length,1);
  size=await page.evaluate(()=>[document.documentElement.scrollWidth,innerWidth]);
  assert(size[0]<=size[1],'Mobile horizontal overflow after operations '+size);
  const output=path.join(root,'ui-review-screenshots');fs.mkdirSync(output,{recursive:true});
  await page.screenshot({path:path.join(output,'crates-mobile-390.png'),fullPage:true});
  await page.setViewportSize({width:1440,height:900});
  await page.screenshot({path:path.join(output,'crates-desktop-1440.png'),fullPage:true});
  const wide=await page.evaluate(()=>[document.documentElement.scrollWidth,innerWidth]);
  assert(wide[0]<=wide[1],'Desktop horizontal overflow '+wide);
  assert.deepEqual(errors,[],'No JavaScript exceptions');
  console.log(JSON.stringify({status:'PASS',mobile:size,desktop:wide,receipts:rec.length,crates:state.crates.length,weighs:state.weighs.size,transfers:state.transfers.size,printers:state.printers.length,screenshots:output},null,2));
  await page.close();
 }finally{await b.close();await new Promise(resolve=>localServer.close(resolve))}
}
main().catch(err=>{console.error('E2E FAILED',err);process.exitCode=1});
