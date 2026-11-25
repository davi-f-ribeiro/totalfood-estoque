const {chromium}=require('playwright');
// Fluxo completo como usuário, em celular (390 px): cozinha pede, estoque separa e entrega,
// cozinha recebe com diferença e concilia. Exige o servidor rodando com um banco de teste
// e os usuários 'cozinha.central' e 'estoque.central' (perfis Cozinha e Estoquista).
// Variáveis: BASE_URL (ex.: http://127.0.0.1:8099), SENHA_TESTE, SHOTS (pasta das capturas).
const B=process.env.BASE_URL||'http://127.0.0.1:8080', SHOTS=process.env.SHOTS||'.';
const log=[];const step=(who,what,ok,extra='')=>{log.push({who,what,ok,extra});console.log((ok?'OK  ':'FAIL')+' ['+who+'] '+what+(extra?' — '+extra:''))};
async function login(ctx,user){const p=await ctx.newPage();p.on('dialog',d=>d.accept());await p.goto(B+'/login');await p.fill('input[name=login]',user);await p.fill('input[name=password]',process.env.SENHA_TESTE||"Senha-teste-1");await p.click('button[type=submit]');await p.waitForURL(u=>!u.pathname.startsWith('/login'),{timeout:10000});return p}
(async()=>{
 const b=await chromium.launch({headless:true});
 const mk=()=>b.newContext({viewport:{width:390,height:844},deviceScaleFactor:1});
 const cozCtx=await mk(); const coz=await login(cozCtx,'cozinha.central');
 await coz.goto(B+'/material-requests',{waitUntil:'networkidle'});
 const tabOk=await coz.locator('#tabCreate').isVisible(); step('cozinha','aba Nova requisição visível',tabOk);
 await coz.click('#tabCreate');
 let reqId='';
 await coz.fill('#materialUnit','Cozinha Central');
 await coz.fill('#materialSearch','feijão');
 try{await coz.locator('#materialMatches button',{hasText:'Feijão Carioca'}).first().click({timeout:5000});step('cozinha','escolhe o material na busca',true)}catch(e){step('cozinha','escolhe o material na busca',false,e.message.split('\n')[0])}
 const focused=await coz.evaluate(()=>document.activeElement&&document.activeElement.closest('#materialDraft')!==null);
 step('cozinha','foco vai para a quantidade após escolher',focused);
 await coz.fill('#materialDraft input','10');
 await coz.selectOption('#materialService',{label:'Almoço'});
 await coz.click('#materialCreateSubmit');
 try{await coz.locator('#materialCreateStatus',{hasText:'enviada ao estoque'}).waitFor({timeout:8000});const msg=await coz.locator('#materialCreateStatus').textContent();reqId=(msg.match(/#(\d+)/)||[])[1]||'';step('cozinha','envia a requisição',true,msg)}catch(e){step('cozinha','envia a requisição',false,(await coz.locator('#materialCreateStatus').textContent().catch(()=>'')))}
 const mine=()=>coz.locator('#materialList button[data-request-id="'+reqId+'"]');
 await coz.screenshot({path:SHOTS+'/1-cozinha-enviou.png',fullPage:false});
 await coz.click('#tabList'); await coz.waitForTimeout(400);
 const row1=await mine().textContent().catch(()=> '');
 step('cozinha','lista mostra a pendência de quem vai agir após o pedido',/Estoque: falta separar/.test(row1),row1.replace(/\s+/g,' ').slice(0,200));
 // Estoquista
 const estCtx=await mk(); const est=await login(estCtx,'estoque.central');
 await est.goto(B+'/material-requests',{waitUntil:'networkidle'});
 await est.locator('#materialList button[data-request-id="'+reqId+'"]').click();
 await est.locator('#materialDetail').waitFor({state:'visible'});
 const lineCard=est.locator('[data-note-key^="line:"]').first();
 try{await lineCard.locator('button',{hasText:'Separar material'}).click();
  await lineCard.locator('select').waitFor();
  await lineCard.locator('input').first().fill('10');
  await lineCard.locator('button',{hasText:'Confirmar separação'}).click();
  await lineCard.locator('[data-detail-note]',{hasText:'separados'}).waitFor({timeout:8000});step('estoque','separa 10 kg',true,await lineCard.locator('[data-detail-note]').textContent())}catch(e){step('estoque','separa 10 kg',false,e.message.split('\n')[0])}
 await est.screenshot({path:SHOTS+'/2-estoque-separou.png',fullPage:false});
 try{const res=est.locator('[data-note-key^="res:"]').first();
  await res.locator('button',{hasText:'Confirmar entrega'}).click();
  await est.locator('[data-note-key="deliveries"] [data-detail-note]',{hasText:'registrada'}).waitFor({timeout:8000});step('estoque','registra a entrega',true,await est.locator('[data-note-key="deliveries"] [data-detail-note]').textContent())}catch(e){step('estoque','registra a entrega',false,e.message.split('\n')[0])}
 await est.click('#materialRefresh'); await est.waitForTimeout(500);
 step('estoque','lista mostra quem precisa agir (cozinha confirma o recebimento)',/Cozinha: falta confirmar o recebimento/.test(await est.locator('#materialList button[data-request-id="'+reqId+'"]').textContent()),(await est.locator('#materialList button').first().textContent()).replace(/\s+/g,' ').slice(0,160));
 // Cozinha recebe com diferença
 await coz.click('#tabList'); await coz.click('#materialRefresh'); await coz.waitForTimeout(500);
 const row2=(await mine().textContent()).replace(/\s+/g,' ');
 step('cozinha','lista cobra o recebimento depois da entrega',/Cozinha: falta confirmar o recebimento de 1 entrega/.test(row2),row2.slice(0,200));
 await mine().click(); await coz.locator('#materialDetail').waitFor({state:'visible'});
 const dCard=coz.locator('[data-note-key^="delivery:"]').first();
 const gotInput=dCard.locator('input').first();
 await gotInput.fill('8');
 const motive=dCard.locator('label',{hasText:'Motivo da diferença'});
 const motiveVisible=await motive.isVisible();
 step('cozinha','campo de motivo aparece só ao receber menos',motiveVisible);
 await motive.locator('input').fill('Caixa avariada');
 try{await dCard.locator('button',{hasText:'Confirmar recebimento'}).click();await coz.locator('[data-note-key^="delivery:"] [data-detail-note]',{hasText:'Faltam'}).first().waitFor({timeout:8000});step('cozinha','recebe 8 de 10 com motivo',true,await coz.locator('[data-note-key^="delivery:"] [data-detail-note]').first().textContent())}catch(e){step('cozinha','recebe 8 de 10 com motivo',false,e.message.split('\n')[0])}
 await coz.click('#materialRefresh');await coz.waitForTimeout(500);
 const row3=(await mine().textContent()).replace(/\s+/g,' ');
 step('cozinha','lista cobra o restante',/falta confirmar o restante de 1 entrega/.test(row3),row3.slice(0,200));
 try{const rem=coz.locator('[data-note-key^="delivery:"]').first();
  await rem.locator('input').nth(1).fill('2');
  await rem.locator('button',{hasText:'Confirmar recebimento do restante'}).click();
  await coz.locator('[data-note-key^="delivery:"] [data-detail-note]',{hasText:'recebida por inteiro'}).first().waitFor({timeout:8000});step('cozinha','confirma o restante',true)}catch(e){step('cozinha','confirma o restante',false,e.message.split('\n')[0])}
 await coz.click('#materialRefresh');await coz.waitForTimeout(500);
 const row4=(await mine().textContent()).replace(/\s+/g,' ');
 step('cozinha','lista sem pendência no fim',!/falta/.test(row4),row4.slice(0,200));
 await coz.screenshot({path:SHOTS+'/3-cozinha-fim.png',fullPage:false});
 // Overflow check
 const ov=await coz.evaluate(()=>document.documentElement.scrollWidth>innerWidth);step('cozinha','sem rolagem lateral a 390 px',!ov);
 await b.close();
 if(log.some(x=>!x.ok))process.exitCode=1;
})().catch(e=>{console.error('CRASH',e.message);process.exitCode=1});
