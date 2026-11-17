(() => {
'use strict';
const root=document.getElementById('materialsRoot');if(!root)return;
const $=id=>document.getElementById(id);
const canWrite=root.dataset.canWrite==='1',canFulfill=root.dataset.canFulfill==='1',canReceive=root.dataset.canReceive==='1';
const status=$('materialStatus'),pendingBox=$('materialPending'),list=$('materialList'),detail=$('materialDetail'),detailContent=$('materialDetailContent');
const serviceLabel={cafe:'Café da manhã',almoco:'Almoço',lanche:'Lanche',jantar:'Jantar'};
const statusText=s=>s==='RECEBIDA_COM_DIVERGENCIA'?'RECEBIDA COM DIVERGÊNCIA':String(s||'').replaceAll('_',' ');
let draft=[],current=0,busy=false,pending=null,searchSequence=0,searchTimer=0;
const pendingKey='material-request:pending:'+document.querySelector('meta[name="current-user-id"]')?.content;
const offline=window.OfflineStore?new OfflineStore():null;
const day=s=>s?s.split('-').reverse().join('/'):'—';
const number=s=>new Intl.NumberFormat('pt-BR',{maximumFractionDigits:3}).format(Number(s)||0);
const stamp=s=>s?new Date(s).toLocaleString('pt-BR'):'—';
const el=(tag,text,cls)=>{const e=document.createElement(tag);if(text!==undefined&&text!==null)e.textContent=String(text);if(cls)e.className=cls;return e};
const field=(label,input)=>{const e=el('label');e.append(el('span',label),input);return e};
const btn=(text,click,secondary=true)=>{const b=el('button',text,secondary?'secondary':'');b.type='button';b.addEventListener('click',click);return b};
// Avisos ficam junto do que foi feito (contrato global, seção 3): formulário
// de criação, lista, ou o cartão do material/separação/entrega no detalhe.
// O aviso do topo (#materialStatus) fica só para a operação pendente
// reenviada pela caixa do topo.
function note(type,msg){setFeedback(status,type,msg)}
function say(target,type,msg,retry){
 if(!target)return note(type,msg);
 setFeedback(target,type,msg);
 if(retry&&type==='error'&&msg){const b=btn('Enviar de novo',()=>sendPending(retry),false);b.classList.add('material-retry-inline');target.append(' ',b)}
 if(type&&!target.hidden)target.scrollIntoView({block:'nearest'});
}
// O detalhe é redesenhado depois de cada ação; o aviso é recolocado no
// cartão com a mesma chave, ou no topo do detalhe se o cartão sumiu.
let detailNote=null;
function detailSay(key,type,msg,retry){detailNote={request:current,key,type,msg,retry};placeDetailNote()}
function placeDetailNote(){
 detailContent.querySelectorAll('[data-detail-note]').forEach(x=>x.remove());
 if(!detailNote||detailNote.request!==current||detailNote.type==='')return;
 const anchor=detailContent.querySelector('[data-note-key="'+detailNote.key+'"]')||detailContent.querySelector('[data-note-key="request"]');
 if(!anchor)return;
 const p=el('p',null,'operation-feedback');p.dataset.detailNote='1';p.setAttribute('role','status');p.setAttribute('aria-live','polite');
 anchor.append(p);say(p,detailNote.type,detailNote.msg,detailNote.retry);
}
function report(where,type,msg,retry){
 if(where&&where.kind==='create')return say($('materialCreateStatus'),type,msg,retry);
 if(where&&where.kind==='list')return say($('materialListStatus'),type,msg,retry);
 if(where&&where.kind==='detail')return detailSay(type==='success'&&where.successKey?where.successKey:where.key,type,msg,retry);
 note(type,msg);
}
function showPending(){pendingBox.hidden=!pending;$('materialRetry').disabled=busy}
function getStored(){try{let p=JSON.parse(sessionStorage.getItem(pendingKey)||'null');return p&&p.url&&p.body?p:null}catch(e){return null}}
async function loadPending(){
 if(offline){
  try{
   const old=await offline.getPending('material-request');
   if(old)pending={url:old.endpoint,body:old.payload,operation_id:old.operation_id};
  }catch(e){}
 }
 if(!pending)pending=getStored();
 if(pending){
  pending.operation_id=pending.operation_id||pending.body.operation_id||newOperationID();
  sessionStorage.setItem(pendingKey,JSON.stringify(pending));
  if(offline)try{await offline.claimPending('material-request',pending.operation_id,pending.url,pending.body)}catch(e){}
 }
 showPending();
}
const pendingReady=loadPending();
async function clearPending(sent){
 if(offline)try{await offline.clearPending('material-request',sent.operation_id)}catch(e){}
 sessionStorage.removeItem(pendingKey);pending=null;showPending();
}
// opts: {label, loading, success(resp), failure, where}
async function run(url,body,opts){
 await pendingReady;
 if(busy||pending){
  const what=pending&&pending.label?pending.label:'uma operação anterior';
  report(opts.where,'error','Ainda falta confirmar '+what+'. Envie de novo antes de continuar.',pending?{where:opts.where}:null);
  return false;
 }
 const fresh={url,body,operation_id:body.operation_id||newOperationID(),label:opts.label};
 if(offline)try{
  const claimed=await offline.claimPending('material-request',fresh.operation_id,url,body);
  if(!claimed.claimed){
   pending={url:claimed.record.endpoint,body:claimed.record.payload,operation_id:claimed.record.operation_id};
   sessionStorage.setItem(pendingKey,JSON.stringify(pending));showPending();
   report(opts.where,'error','Existe uma operação sem confirmação neste dispositivo. Envie de novo antes de continuar.',{where:opts.where});
   return false;
  }
 }catch(e){}
 pending=fresh;sessionStorage.setItem(pendingKey,JSON.stringify(pending));showPending();
 return sendPending(opts);
}
async function sendPending(opts={}){
 await pendingReady;
 if(!pending||busy)return false;
 const where=opts.where,sent=pending;
 const what=sent.label||'a operação anterior';
 busy=true;showPending();report(where,'loading',opts.loading||('Conferindo '+what+'…'));
 try{
  const resp=await api(sent.url,{method:'POST',body:JSON.stringify(sent.body),timeoutMs:movementSendTimeoutMs});
  await clearPending(sent);
  if(sent.url==='/api/material-requests'&&canWrite){
   draft=[];
   $('materialDraft').textContent='';
   $('materialCreateForm').reset();
   $('materialDeliveryDate').value=todayProductionDate();
   $('materialProductionDate').value=todayProductionDate();
  }
  const done=opts.success?opts.success(resp||{}):(what.charAt(0).toUpperCase()+what.slice(1)+' confirmada.');
  if(where&&where.kind==='detail')detailNote={request:current,key:where.successKey||where.key,type:'success',msg:done};
  await refresh();
  report(where,'success',done);
  return true;
 }catch(e){
  if(e.status>=400&&e.status<500){
   await clearPending(sent);
   report(where,'error',uiErrorMessage(e,opts.failure||('Não foi possível concluir '+what+'.')));
  }else{
   report(where,'error','Sem confirmação do servidor para '+what+'. Envie de novo: o mesmo lançamento é conferido sem duplicar.',{where,success:opts.success,failure:opts.failure});
  }
  return false;
 }finally{busy=false;showPending()}
}
$('materialRetry').addEventListener('click',()=>sendPending({}));
async function refresh(){
 try{
  const records=await api('/api/material-requests');
  list.textContent='';
  if(!records.length)list.append(el('p','Nenhuma requisição encontrada.','hint'));
  records.forEach(r=>{
   const b=btn('',()=>openReq(r.id));
   b.textContent='';
   const h=el('strong','#'+r.id+' · '+r.unit_name);
   const sub=el('small',day(r.production_date)+' · '+(serviceLabel[r.service]||r.service)+' · '+r.items+' materiais');
   const state=el('span',statusText(r.status),'material-tag');
   b.append(h,sub,state);b.setAttribute('aria-current',String(current===r.id));list.append(b);
  });
  if(current)await openReq(current,false);
 }catch(e){report({kind:'list'},'error',uiErrorMessage(e,'Não foi possível atualizar as requisições.'))}
}
async function openReq(id,scroll=true){
 try{
  const r=await api('/api/material-requests/'+id);
  if(current!==id)detailNote=null;
  current=id;detail.hidden=false;renderDetail(r);
  list.querySelectorAll('button[aria-current]').forEach(b=>b.setAttribute('aria-current',String(b.firstChild?.textContent?.startsWith('#'+id+' · '))));
  if(scroll)detail.scrollIntoView({block:'start',behavior:'auto'});
 }catch(e){report({kind:'list'},'error',uiErrorMessage(e,'Não foi possível abrir a requisição #'+id+'.'))}
}
$('materialRefresh').addEventListener('click',refresh);
$('materialClose').addEventListener('click',()=>{current=0;detailNote=null;detail.hidden=true;list.querySelectorAll('button[aria-current]').forEach(b=>b.setAttribute('aria-current','false'))});
function actionRow(){return el('div',null,'material-actions')}
function numericInput(value){
 const i=el('input');i.type='text';i.inputMode='decimal';i.value=String(value||'');i.placeholder='Quantidade';i.setAttribute('aria-label','Quantidade em unidade-base');return i;
}
function validQuantity(value){const x=String(value||'').trim().replace(',','.');return /^(0|[1-9][0-9]*)(\.[0-9]{1,3})?$/.test(x)&&Number(x)>0}
async function chooseReserve(line,wrap){
 wrap.textContent='';const box=el('div',null,'material-inline');
 const skuSelect=el('select');skuSelect.setAttribute('aria-label','Apresentação e depósito');
 try{
  const options=await api('/api/material-requests/options/'+line.item_id);
  if(!options.length){wrap.append(el('p','Não há estoque disponível de '+line.item+'.','hint'));return}
  options.forEach(o=>{const opt=el('option',o.brand+' · '+o.sku+' · '+o.warehouse+' (livre '+number(o.available)+' / físico '+number(o.stock)+' '+line.unit+')');opt.value=o.sku_id+':'+o.warehouse_id;skuSelect.append(opt)});
  const qty=numericInput((Number(line.quantity)-Number(line.reserved)-Number(line.delivered)).toFixed(3));
  box.append(field('Apresentação e depósito',skuSelect),field('Separar ('+line.unit+')',qty));
  const actions=actionRow();
  actions.append(btn('Confirmar separação',async()=>{
   const where={kind:'detail',key:'line:'+line.id};
   if(!validQuantity(qty.value)){report(where,'error','Informe a quantidade de '+line.item+' maior que zero, com até 3 casas decimais.');return}
   const [sku_id,warehouse_id]=skuSelect.value.split(':').map(Number);
   const o=options.find(x=>x.sku_id===sku_id&&x.warehouse_id===warehouse_id)||{};
   const q=number(requestDecimal(qty.value))+' '+line.unit;
   await run('/api/material-requests/'+current+'/reserve',{operation_id:newOperationID(),line_id:line.id,sku_id,warehouse_id,quantity:requestDecimal(qty.value)},
    {label:'a separação de '+q+' de '+line.item,loading:'Separando '+q+' de '+line.item+'…',where,
     success:()=>q+' de '+line.item+' separados ('+(o.brand||'')+(o.warehouse?', '+o.warehouse:'')+').',failure:'Separação de '+line.item+' recusada.'});
  },false),btn('Fechar',()=>{wrap.textContent=''}));
  wrap.append(box,actions);
 }catch(e){const p=el('p',null,'operation-feedback');wrap.append(p);say(p,'error',uiErrorMessage(e,'Não foi possível consultar o estoque de '+line.item+'.'))}
}
function renderDetail(r){
 detailContent.textContent='';
 const facts=el('div',null,'material-facts');
 [
  ['Unidade',r.unit_name],['Solicitante',r.requested_by],
  ['Produção',day(r.production_date)],['Serviço',serviceLabel[r.service]||r.service],
  ['Entrega desejada',day(r.delivery_date)],['Status',r.status.replaceAll('_',' ')],
  ['Criada em',stamp(r.created_at)]
 ].forEach(([label,value])=>{const d=el('div');d.append(el('small',label),el('strong',value));facts.append(d)});
 detailContent.append(facts);
 if(r.notes)detailContent.append(el('p','Observação: '+r.notes));
 const top=el('div');top.dataset.noteKey='request';
 if(canWrite&&!r.deliveries.length&&r.status!=='CANCELADA'){
  const actions=actionRow();
  actions.append(btn('Cancelar requisição',async()=>{
   if(!window.confirm('Cancelar esta requisição e liberar as reservas?'))return;
   await run('/api/material-requests/'+r.id+'/cancel',{},{label:'o cancelamento da requisição #'+r.id,loading:'Cancelando a requisição #'+r.id+'…',
    where:{kind:'detail',key:'request'},success:()=>'Requisição #'+r.id+' cancelada e reservas liberadas.',failure:'Cancelamento da requisição #'+r.id+' recusado.'});
  }));
  top.append(actions);
 }
 // Itens sem peso padrão (hortifruti, proteína): a conta exata não fecha. Depois de
 // conferir as entregas, a cozinha pode aceitar o que chegou e encerrar a requisição.
 if(r.closed_at)top.append(el('p','Encerrada por '+r.closed_by+' em '+stamp(r.closed_at)+', aceitando o que foi entregue.'+(r.close_notes?' '+r.close_notes:''),'material-small'));
 const allChecked=r.deliveries.length>0&&r.deliveries.every(d=>d.received);
 if(canReceive&&allChecked&&!r.closed_at&&r.status!=='RECEBIDA'&&r.status!=='CANCELADA'){
  const obs=el('input');obs.maxLength=500;obs.autocomplete='off';
  top.append(el('p','O que chegou é diferente do pedido. Se atende, aceite a diferença e encerre a requisição.','material-small'),field('Observação (opcional)',obs),btn('Aceitar e encerrar',async()=>{
   if(!window.confirm('Aceitar o que foi recebido e encerrar esta requisição? O que faltou não será mais entregue.'))return;
   await run('/api/material-requests/'+r.id+'/close',{notes:obs.value.trim()},{label:'o encerramento da requisição #'+r.id,loading:'Encerrando a requisição #'+r.id+'…',
    where:{kind:'detail',key:'request'},success:()=>'Requisição #'+r.id+' encerrada com o que foi recebido.',failure:'Encerramento da requisição #'+r.id+' recusado.'});
  },false));
 }
 detailContent.append(top);
 detailContent.append(el('h3','Materiais'));
 r.lines.forEach(line=>{
  const card=el('article',null,'material-line-details');card.dataset.noteKey='line:'+line.id;
  card.append(el('strong',line.item),el('p','Solicitado '+number(line.quantity)+' '+line.unit+' · Separado '+number(line.reserved)+' · Entregue '+number(line.delivered),'material-small'));
  const available=Number(line.quantity)-Number(line.reserved)-Number(line.delivered);
  if(canFulfill&&available>0.0001&&r.status!=='CANCELADA'&&r.status!=='RECEBIDA'&&r.status!=='RECEBIDA_COM_DIVERGENCIA'){
   const wrap=el('div');card.append(btn('Separar material',()=>chooseReserve(line,wrap)));card.append(wrap);
  }
  detailContent.append(card);
 });
 if(r.reservations.length){
  detailContent.append(el('h3','Separações'));
  r.reservations.forEach(res=>{
   const card=el('article',null,'material-reservation');card.dataset.noteKey='res:'+res.id;
   const line=r.lines.find(x=>x.id===res.line_id);
   const item=line?.item||'Material',unit=line?.unit||'';
   card.append(el('strong',(line?.item||'Material')+' · '+res.brand),el('p',res.sku+' · '+res.warehouse,'material-small'),
     el('p','Reservado '+number(res.quantity)+' · Saldo separado '+number(res.remaining)+' '+(line?.unit||'')+' · '+res.status,'material-small'));
   if(canFulfill&&res.status==='ATIVA'&&Number(res.remaining)>0){
    const input=numericInput(res.remaining);
    // Item controlado por caixa (hortifruti): o estoquista marca as caixas que vão sair e a
    // quantidade vira a soma dos pesos atuais. Uma caixa só pode sair em parte.
    const crateBox=el('div',null,'material-crates'),crateWeights={};crateBox.hidden=true;
    const pickedCrates=()=>[...crateBox.querySelectorAll('input:checked')].map(x=>Number(x.value));
    const syncCrates=()=>{const ids=pickedCrates();input.readOnly=ids.length>1;if(!ids.length)return;input.value=String(Math.round(ids.reduce((t,i)=>t+Number(crateWeights[i]),0)*1000)/1000)};
    api('/api/crates?warehouse_id='+res.warehouse_id).then(cs=>{
     const mine=cs.filter(c=>c.sku_id===res.sku_id);if(!mine.length)return;
     crateBox.append(el('p','Caixas que vão sair. O peso é somado; para tirar só parte, marque uma caixa e ajuste a quantidade.','material-small'));
     mine.forEach(c=>{crateWeights[c.id]=c.weight;const l=el('label',null,'check'),cb=el('input');cb.type='checkbox';cb.value=c.id;cb.addEventListener('change',syncCrates);l.append(cb,el('span',c.code+', '+number(c.weight)+' kg'));crateBox.append(l)});
     crateBox.hidden=false;
    }).catch(()=>{});
    const actions=actionRow();
    actions.append(btn('Confirmar entrega',async()=>{
     const where={kind:'detail',key:'res:'+res.id,successKey:'deliveries'};
     if(!validQuantity(input.value)){report(where,'error','Informe a quantidade de '+item+' a entregar, maior que zero e com até 3 casas decimais.');return}
     if(!crateBox.hidden&&!pickedCrates().length){report(where,'error','Este produto é controlado por caixas. Marque as caixas que vão sair.');return}
     const q=number(requestDecimal(input.value))+' '+unit;
     await run('/api/material-requests/'+r.id+'/deliver',Object.assign({operation_id:newOperationID(),reservation_id:res.id,quantity:requestDecimal(input.value)},pickedCrates().length?{crate_ids:pickedCrates()}:{}),
      {label:'a entrega de '+q+' de '+item,loading:'Registrando a saída de '+q+' de '+item+'…',where,
       success:d=>'Entrega de '+q+' de '+item+' registrada'+(d.movement_id?' (saída #'+d.movement_id+')':'')+'. Falta a cozinha confirmar o recebimento.',failure:'Entrega de '+item+' recusada.'});
    },false),btn('Liberar reserva',async()=>{
     if(!window.confirm('Liberar o saldo não entregue desta separação?'))return;
     await run('/api/material-requests/'+r.id+'/reservations/'+res.id+'/release',{},{label:'a liberação da reserva de '+item,loading:'Liberando a reserva de '+item+'…',
      where:{kind:'detail',key:'res:'+res.id},success:()=>'Reserva de '+item+' ('+res.brand+') liberada; o saldo voltou a ficar livre.',failure:'Liberação da reserva de '+item+' recusada.'});
    }));
    card.append(crateBox,field('Quantidade a entregar',input),actions);
   }
   detailContent.append(card);
  });
 }
 if(r.deliveries.length){
  const dh=el('div');dh.dataset.noteKey='deliveries';dh.append(el('h3','Entregas e recebimento'));detailContent.append(dh);
  r.deliveries.forEach(d=>{
   const card=el('article',null,'material-delivery');card.dataset.noteKey='delivery:'+d.id;
   const pendingDiv=Number(d.pending_divergence)||0;
   // A entrega diz o que é: material, quantidade com unidade, marca/apresentação e depósito.
   const dres=r.reservations.find(x=>x.id===d.reservation_id),dline=dres?r.lines.find(x=>x.id===dres.line_id):null;
   const dunit=dline?.unit||'unidades-base',dwhat=dline?dline.item:'';
   card.append(el('strong',dwhat?dwhat+', '+number(d.quantity)+' '+dunit:'Entrega #'+d.id+' · '+number(d.quantity)+' '+dunit));
   if(dres)card.append(el('p',[dres.brand,dres.sku,dres.warehouse].filter(Boolean).join(', '),'material-small'));
   card.append(el('p','Entrega #'+d.id+', saída #'+d.movement_id+', '+stamp(d.delivered_at)+', por '+d.delivered_by,'material-small'));
   if((d.crates||[]).length)card.append(el('p','Caixas: '+d.crates.map(c=>c.code+' ('+number(c.quantity)+' kg)').join(', '),'material-small'));
   if(!d.received)card.append(el('p','Aguardando recebimento','material-small'));
   else if(pendingDiv<=0&&(d.receipts||[]).length<2)card.append(el('p','Recebimento confirmado por '+d.received_by,'material-small'));
   else card.append(el('p','Recebido '+number(d.received_quantity)+' de '+number(d.quantity)+(dline?' '+dunit:'')+', conferido por '+d.received_by,'material-small'));
   if(d.received&&pendingDiv>0){
    card.append(el('p','Faltam '+number(d.pending_divergence)+(dline?' '+dunit:'')+' para receber','material-divergence'));
    if(d.divergence_reason)card.append(el('p','Motivo informado: '+d.divergence_reason,'material-small'));
   }
   (d.receipts||[]).filter(x=>x.kind==='CONCILIACAO').forEach(x=>card.append(el('p','Recebido depois: '+number(x.quantity)+', registrado por '+x.by+' em '+stamp(x.at)+(x.notes?'. '+x.notes:''),'material-small')));
   if(canReceive&&!d.received){
    // A cozinha informa quanto recebeu. Menos que o entregue pede o motivo e deixa a
    // diferença como divergência, até o restante chegar e ser confirmado aqui mesmo.
    const got=numericInput(d.quantity),why=el('input');why.maxLength=500;why.autocomplete='off';
    const where={kind:'detail',key:'delivery:'+d.id};
    card.append(field('Quantidade recebida'+(dline?' ('+dunit+')':''),got),field('Motivo da diferença (só se recebeu menos)',why),btn('Confirmar recebimento',async()=>{
     const raw=String(got.value||'').trim().replace(',','.');
     if(!/^(0|[1-9][0-9]*)(\.[0-9]{1,3})?$/.test(raw)){report(where,'error','Informe a quantidade recebida, com até 3 casas decimais.');return}
     const q=Number(raw),total=Number(d.quantity);
     if(q>total){report(where,'error','A quantidade recebida não pode ser maior que a entregue ('+number(d.quantity)+').');return}
     const partial=q<total,reason=why.value.trim();
     if(partial&&!reason){report(where,'error','Informe o motivo da diferença entre o entregue e o recebido.');return}
     if(!window.confirm(partial?'Confirmar o recebimento de '+number(raw)+' de '+number(d.quantity)+(dline?' '+dunit+' de '+dwhat:'')+'? A diferença fica pendente até o restante chegar.':(dwhat?'Você recebeu '+number(d.quantity)+' '+dunit+' de '+dwhat+'?':'Você recebeu o material desta entrega?')))return;
     await run('/api/material-requests/'+r.id+'/deliveries/'+d.id+'/receive',partial?{received_quantity:raw,reason}:{received_quantity:raw},{label:'o recebimento da entrega #'+d.id,loading:'Confirmando o recebimento da entrega #'+d.id+'…',
      where,success:x=>partial?'Recebimento de '+number(raw)+' de '+number(d.quantity)+' registrado. Faltam '+number(x.pending_divergence)+'; confirme aqui quando o restante chegar.':'Recebimento da entrega #'+d.id+' confirmado.',failure:'Recebimento da entrega #'+d.id+' recusado.'});
    },false));
   }
   if(canReceive&&d.received&&pendingDiv>0&&r.status!=='CANCELADA'){
    const more=numericInput(d.pending_divergence),obs=el('input');obs.maxLength=500;obs.autocomplete='off';
    const where={kind:'detail',key:'delivery:'+d.id};
    card.append(field('Quantidade recebida depois'+(dline?' ('+dunit+')':''),more),field('Observação',obs),btn('Confirmar recebimento do restante',async()=>{
     if(!validQuantity(more.value)){report(where,'error','Informe a quantidade recebida depois, maior que zero e com até 3 casas decimais.');return}
     const q=requestDecimal(more.value);
     if(Number(q)>pendingDiv){report(where,'error','Só faltam '+number(d.pending_divergence)+' para receber nesta entrega.');return}
     await run('/api/material-requests/'+r.id+'/deliveries/'+d.id+'/reconcile',{operation_id:newOperationID(),quantity:q,notes:obs.value.trim()},{label:'o recebimento do restante da entrega #'+d.id,loading:'Registrando o recebimento do restante da entrega #'+d.id+'…',
      where,success:x=>Number(x.pending_divergence)>0?'Recebimento de '+number(q)+' registrado. Ainda faltam '+number(x.pending_divergence)+'.':'Entrega #'+d.id+' recebida por inteiro.',failure:'Recebimento do restante da entrega #'+d.id+' recusado.'});
    },false));
   }
   detailContent.append(card);
  });
 }
 if(r.events.length){
  const events=el('details'),sum=el('summary','Histórico auditável');events.append(sum);
  r.events.forEach(x=>events.append(el('div',stamp(x.at)+' · '+x.by+' · '+x.action+' · '+x.details,'material-event')));
  detailContent.append(events);
 }
 placeDetailNote();
}
if(canWrite){
 const form=$('materialCreateForm'),search=$('materialSearch'),matches=$('materialMatches'),draftHost=$('materialDraft');
 $('materialDeliveryDate').value=todayProductionDate();$('materialProductionDate').value=todayProductionDate();
 function renderDraft(){
  draftHost.textContent='';
  draft.forEach(x=>{
   const row=el('div',null,'material-line'),head=el('div',null,'material-line-header');
   head.append(el('strong',x.name+' · '+x.unit),btn('Remover',()=>{draft=draft.filter(d=>d.id!==x.id);renderDraft()}));
   const inp=numericInput(x.quantity);inp.value=x.quantity;
   inp.addEventListener('input',()=>{x.quantity=inp.value});
   row.append(head,field('Quantidade ('+x.unit+')',inp));draftHost.append(row);
  });
 }
 async function lookup(q){
  const seq=++searchSequence;matches.textContent='';
  if(q.trim().length<2)return;
  try{
   const items=await api('/api/material-requests/items?q='+encodeURIComponent(q.trim()));
   if(seq!==searchSequence)return;
   if(!items.length){matches.append(el('p','Material não encontrado.','hint'));return}
   items.filter(item=>!draft.some(x=>x.id===item.id)).forEach(item=>{
    matches.append(btn(item.name+' · '+item.unit,()=>{draft.push({...item,quantity:''});search.value='';matches.textContent='';renderDraft()}));
   });
  }catch(e){if(seq===searchSequence)matches.append(el('p',uiErrorMessage(e,'Não foi possível buscar materiais.'),'hint'))}
 }
 search.addEventListener('input',()=>{clearTimeout(searchTimer);searchTimer=setTimeout(()=>lookup(search.value),250)});
 form.addEventListener('submit',async e=>{
  e.preventDefault();
  const where={kind:'create'};
  if(!form.checkValidity()){form.reportValidity();return}
  if(!draft.length){report(where,'error','Adicione pelo menos um material à requisição.');return}
  const bad=draft.find(x=>!validQuantity(x.quantity));
  if(bad){report(where,'error','Informe a quantidade de '+bad.name+' maior que zero, com até 3 casas decimais.');return}
  const body={operation_id:newOperationID(),unit_name:$('materialUnit').value.trim(),
   delivery_date:$('materialDeliveryDate').value,production_date:$('materialProductionDate').value,
   service:$('materialService').value,notes:$('materialNotes').value.trim(),
   lines:draft.map(x=>({item_id:x.id,quantity:requestDecimal(x.quantity)}))};
  const n=draft.length;
  await run('/api/material-requests',body,{label:'a requisição de '+body.unit_name,loading:'Enviando a requisição de '+body.unit_name+'…',where,
   success:x=>'Requisição'+(x.id?' #'+x.id:'')+' enviada ao estoque: '+n+(n===1?' material':' materiais')+' para '+body.unit_name+'.',failure:'Requisição recusada.'});
 });
}
refresh();
})();