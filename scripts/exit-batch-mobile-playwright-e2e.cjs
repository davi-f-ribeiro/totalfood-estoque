'use strict';
// Saída em lote no celular (390 px), com a API e a câmera simuladas.
// O operador lê vários códigos com a câmera, ajusta as quantidades com − e +
// e confirma tudo de uma vez. Os avisos ficam junto da ação (contrato global,
// seção 3): a confirmação na barra fixa acima do menu e a leitura junto da busca.
// No desktop a Saída continua em tabela, e a Entrada no celular continua no
// assistente de 3 passos.
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const {chromium}=require('playwright');
const root=path.resolve(__dirname,'..');
const template=fs.readFileSync(path.join(root,'cmd/server/templates/movement.html'),'utf8');
const page_html=title=>template.replace('{{template "header" .}}','<header class="top">Gestão de Estoque</header>')
 .replace('{{template "nav" .}}','<nav class="nav"><a href="/products">Produtos</a></nav>')
 .replaceAll('{{.CSRF}}','e2e-token').replaceAll('{{.Title}}',title)
 .replace(/\{\{if \.Can(Entry|Exit|Transfer)\}\}/g,'')
 .replace(/\{\{if eq \.Title "([^"]+)"\}\} aria-current="page"\{\{end\}\}/g,(_,t)=>t===title?' aria-current="page"':'')
 .replaceAll('{{end}}','');

const FEIJAO={sku_id:5,item:'Feijão carioca',brand:'Kicaldo',barcode:'7891234567890',unit:'KG',content_quantity:'1'};
const ARROZ={sku_id:6,item:'Arroz branco',brand:'Tio João',barcode:'7890000000017',unit:'KG',content_quantity:'5'};
const stock={5:'10',6:'20'};
const posts=[];
const respond=(route,status,body)=>route.fulfill({status,contentType:'application/json',body:JSON.stringify(body)});

async function main(){
 const server=http.createServer((req,res)=>{
  const u=new URL(req.url,'http://127.0.0.1');
  const candidate=path.resolve(root,'cmd/server'+u.pathname),staticRoot=path.resolve(root,'cmd/server/static');
  if(!u.pathname.startsWith('/static/')||!candidate.startsWith(staticRoot+path.sep)||!fs.existsSync(candidate)){res.writeHead(404);res.end();return}
  res.writeHead(200,{'Content-Type':candidate.endsWith('.js')?'application/javascript':candidate.endsWith('.css')?'text/css':'application/octet-stream'});
  fs.createReadStream(candidate).pipe(res);
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const base='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_BIN,args:['--no-sandbox']});
 const shots=process.env.UI_SHOTS||'';
 try{
  const open=async(viewport,pathname,title)=>{
   const page=await browser.newPage({viewport,deviceScaleFactor:1});
   const errors=[];page.on('pageerror',e=>errors.push(e.message));
   // Câmera simulada: um vídeo de canvas e um BarcodeDetector que devolve o próximo código da fila.
   await page.addInitScript(()=>{
    window.__codes=[];
    navigator.mediaDevices.getUserMedia=async()=>{const c=document.createElement('canvas');c.width=320;c.height=240;const g=c.getContext('2d');setInterval(()=>{g.fillStyle='#'+Math.floor(Math.random()*0xffffff).toString(16).padStart(6,'0');g.fillRect(0,0,320,240)},50);return c.captureStream(10)};
    window.BarcodeDetector=class{constructor(){}static async getSupportedFormats(){return['ean_13']}async detect(){const c=window.__codes.shift();return c?[{rawValue:c}]:[]}};
   });
   await page.route('**'+pathname,r=>r.request().isNavigationRequest()?r.fulfill({status:200,contentType:'text/html; charset=utf-8',body:page_html(title)}):r.continue());
   await page.route('**/api/**',async route=>{
    const req=route.request(),u=new URL(req.url()),p=u.pathname;
    if(p==='/api/warehouses')return respond(route,200,[{id:1,name:'Câmara Fria'}]);
    if(p==='/api/products/lookup'){const q=(u.searchParams.get('q')||'').toLowerCase();return respond(route,200,[FEIJAO,ARROZ].filter(x=>x.barcode===q||x.item.toLowerCase().includes(q)))}
    if(p==='/api/stock/availability'){const id=u.searchParams.get('sku_id');return respond(route,200,[{warehouse_id:1,quantity:stock[id]||'0'}])}
    if(p==='/api/movements/exit/batch'){posts.push(req.postDataJSON());return respond(route,201,{id:77,status:'confirmed'})}
    return respond(route,404,{error:'Unknown route '+p});
   });
   await page.goto(base+pathname,{waitUntil:'networkidle'});
   return {page,errors};
  };
  const inViewport=(page,sel)=>page.locator(sel).first().evaluate(el=>{const r=el.getBoundingClientRect();return r.height>0&&r.top>=-1&&r.bottom<=innerHeight+1});
  const noOverflow=async page=>assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Sem rolagem lateral');

  // ---------- Celular: Saída em lote ----------
  {
   const {page,errors}=await open({width:390,height:844},'/exit','Saída');
   assert.equal(await page.locator('.movement').getAttribute('data-mode'),'batch','A Saída no celular usa o lote');
   assert.equal(await page.locator('#movementForm').isVisible(),false,'O assistente de 3 passos não aparece');
   assert(await page.locator('#batchScan').isVisible(),'Botão da câmera visível no celular');
   assert.notEqual(await page.evaluate(()=>document.activeElement.id),'batchSearch','A busca não recebe foco sozinha (abriria o teclado)');

   if(shots)await page.screenshot({path:path.join(shots,'saida-lote-390-vazio.png'),fullPage:true});
   const scan=async code=>{
    await page.evaluate(c=>window.__codes.push(c),code);
    await page.locator('#batchScan').click();
    await page.locator('#scanner').waitFor({state:'hidden'});
   };
   await scan(FEIJAO.barcode);
   await page.locator('#batchFeedback',{hasText:'Feijão carioca adicionado. 1 produto no lote.'}).waitFor();
   assert(await inViewport(page,'#batchFeedback'),'O aviso da leitura aparece junto da busca');
   await scan(FEIJAO.barcode);
   await page.locator('#batchFeedback',{hasText:'Feijão carioca: 2 no lote.'}).waitFor();
   assert.equal(await page.locator('.batch-row.is-card').count(),1,'Ler o mesmo código soma, não duplica');
   assert.equal(await page.locator('.batch-row.is-card .batch-qty').inputValue(),'2');

   // Busca pelo nome, como no desktop.
   await page.locator('#batchSearch').fill('arroz');
   await page.locator('#batchLookup button',{hasText:'Arroz branco'}).click();
   await page.locator('#batchFeedback',{hasText:'Arroz branco adicionado. 2 produtos no lote.'}).waitFor();
   const arroz=page.locator('.batch-row.is-card',{hasText:'Arroz branco'});
   assert.equal(await arroz.locator('.batch-qty-unit').innerText(),'emb. de 5 KG','A quantidade é em embalagens');
   await arroz.getByRole('button',{name:'Aumentar Arroz branco'}).click();
   await arroz.getByRole('button',{name:'Aumentar Arroz branco'}).click();
   assert.equal(await arroz.locator('.batch-qty').inputValue(),'3');
   await arroz.getByRole('button',{name:'Diminuir Arroz branco'}).click();
   assert.equal(await arroz.locator('.batch-qty').inputValue(),'2');
   await arroz.locator('[data-role="after"]',{hasText:'10 KG'}).waitFor();

   // Cartões sem rolagem lateral; a barra de confirmação fica na tela.
   await noOverflow(page);
   assert(await inViewport(page,'#batchSubmit'),'Botão de confirmar sempre visível');
   assert.equal(await page.locator('#batchSubmit').innerText(),'Confirmar saída de 2 produtos');
   if(shots)await page.screenshot({path:path.join(shots,'saida-lote-390.png'),fullPage:false});

   // Estoque insuficiente: aviso na barra e o cartão marcado.
   const feijao=page.locator('.batch-row.is-card',{hasText:'Feijão carioca'});
   await feijao.locator('.batch-qty').fill('11');
   await page.locator('#batchSubmit').click();
   await page.locator('#batchStatus',{hasText:'Item 1, Feijão carioca: a quantidade é maior que o estoque (10 KG).'}).waitFor();
   assert(await inViewport(page,'#batchStatus'),'Erro de validação junto do botão');
   assert(await feijao.evaluate(el=>el.classList.contains('has-error')),'Cartão com erro marcado');
   assert.equal(posts.length,0,'Nada é enviado com erro');

   await feijao.getByRole('button',{name:'Diminuir Feijão carioca'}).click();
   await page.locator('#batchSubmit').click();
   await page.locator('#batchStatus',{hasText:'Saída de 2 produtos registrada.'}).waitFor();
   assert(await inViewport(page,'#batchStatus'),'Confirmação junto do botão');
   assert.equal(posts.length,1,'Um envio para o lote inteiro');
   const body=posts[0];
   assert.equal(body.quantity_semantics,'presentation');
   assert.equal(body.warehouse_id,1);
   assert.match(body.production_date,/^\d{4}-\d{2}-\d{2}$/);
   assert.deepEqual(body.lines.map(l=>[l.sku_id,l.quantity]),[[5,'10'],[6,'2']]);
   assert.equal(await page.locator('.batch-row.is-card').count(),0,'Lote limpo depois de registrar');
   await page.locator('#batchRecent',{hasText:'Feijão carioca, Arroz branco'}).waitFor();
   await noOverflow(page);
   if(shots)await page.screenshot({path:path.join(shots,'saida-lote-390-ok.png'),fullPage:false});
   assert.deepEqual(errors,[]);
   await page.close();
  }

  // ---------- Celular: Entrada continua no assistente ----------
  {
   const {page,errors}=await open({width:390,height:844},'/entry','Entrada');
   assert.equal(await page.locator('.movement').getAttribute('data-mode'),'wizard','Entrada no celular continua no assistente');
   assert(await page.locator('#movementForm').isVisible());
   assert(await page.locator('#scanProduct').isVisible(),'Câmera do assistente continua disponível');
   assert.deepEqual(errors,[]);
   await page.close();
  }

  // ---------- Desktop: Saída continua em tabela ----------
  {
   const {page,errors}=await open({width:1440,height:900},'/exit','Saída');
   assert.equal(await page.locator('.movement').getAttribute('data-mode'),'batch');
   assert.equal(await page.locator('#batchScan').isVisible(),false,'Sem botão de câmera no desktop');
   await page.locator('#batchSearch').fill(FEIJAO.barcode);
   await page.locator('#batchSearch').press('Enter');
   await page.locator('.batch-head').waitFor();
   assert.equal(await page.locator('.batch-row.is-card').count(),0,'Desktop usa linhas de tabela');
   assert.equal(await page.evaluate(()=>getComputedStyle(document.getElementById('batchSubmit').closest('.batch-actions')).position),'static','Desktop sem barra fixa');
   assert.equal(await page.evaluate(()=>document.activeElement.classList.contains('batch-qty')),true,'Desktop foca a quantidade como antes');
   assert.deepEqual(errors,[]);
   await page.close();
  }
  console.log(JSON.stringify({status:'PASS',posts:posts.length},null,1));
 }finally{await browser.close();server.close()}
}
main().catch(e=>{console.error('FAILED',e&&e.stack||e);process.exitCode=1});
