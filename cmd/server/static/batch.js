function newOperationID(){if(globalThis.crypto&&typeof crypto.randomUUID==="function")return crypto.randomUUID();const b=crypto.getRandomValues(new Uint8Array(16));b[6]=(b[6]&0x0f)|0x40;b[8]=(b[8]&0x3f)|0x80;const h=Array.from(b,x=>x.toString(16).padStart(2,"0")).join("");return h.slice(0,8)+"-"+h.slice(8,12)+"-"+h.slice(12,16)+"-"+h.slice(16,20)+"-"+h.slice(20)}
/* Lote de Entrada, Saída e Transferência (desktop, a partir de 1000 px).
   Usa as funções comuns de app.js (api, skuLabel, productLookup, displayDecimal...).
   O servidor grava o lote inteiro ou nada; aqui só montamos o envio e mostramos o resultado. */
(function(){
'use strict';
const root=document.querySelector('.movement');
if(!root||root.dataset.mode!=='batch')return;

const view=document.getElementById('batchView');
const title=root.dataset.title;
const kind=title==='Entrada'?'entry':title==='Transferência'?'transfer':'exit';
const isEntry=kind==='entry',isTransfer=kind==='transfer';
const verb={entry:'Entrada',exit:'Saída',transfer:'Transferência'}[kind];
const endpoint='/api/movements/'+kind+'/batch';
const $=id=>document.getElementById(id);
const wh=$('batchWarehouse'),to=$('batchWarehouseTo'),search=$('batchSearch'),lookup=$('batchLookup'),feedback=$('batchFeedback'),
  table=$('batchTable'),facts=$('batchFacts'),doc=$('batchDocument'),notes=$('batchNotes'),status=$('batchStatus'),
  submit=$('batchSubmit'),clearBtn=$('batchClear'),addBtn=$('batchAdd'),recentBox=$('batchRecentBox'),recent=$('batchRecent'),
  pendingBox=$('batchPending'),pendingText=$('batchPendingText'),checkBtn=$('batchCheck'),retryBtn=$('batchRetry'),discardBtn=$('batchDiscard');

let lines=[],warehouses=[],seq=0,pending=null,pendingState='',launches=[];

view.hidden=false;
$('batchOriginLabel').textContent=isTransfer?'Origem':'Depósito';
$('batchDestWrap').hidden=!isTransfer;

const num=v=>Number(String(v==null?'':v).replace(',','.'))||0;
const fmt=(v,max=3)=>displayDecimal(String(v),max);
const decimalOK=v=>/^(0|[1-9][0-9]*)([.,][0-9]{1,4})?$/.test(String(v||'').trim());
const plural=(n,one,many)=>n+' '+(n===1?one:many);

function note(type,text){setFeedback(status,type,text)}
function lookupNote(type,html){feedback.className='product-feedback '+(type||'');feedback.innerHTML=html||'';feedback.hidden=!html}
function hideLookup(){lookup.textContent='';lookup.hidden=true}

/* ---------- Estoque por depósito ---------- */
function balanceOf(line,warehouseId){
  const row=(line.rows||[]).find(r=>String(r.warehouse_id)===String(warehouseId));
  return row?num(row.quantity):0;
}
function originId(){return wh.value}
function currentBalance(line){return line.rows?balanceOf(line,originId()):null}
function afterBalance(line){
  const bal=currentBalance(line);if(bal===null)return null;
  const q=num(line.qty)*(num(line.content)||1);
  return isEntry?bal+q:bal-q;
}

/* ---------- Tabela ---------- */
function cell(role,cls){const d=document.createElement('div');d.setAttribute('role',role);if(cls)d.className=cls;return d}
function columns(){return isEntry?'minmax(160px,1fr) 88px 96px 104px 88px 40px':'minmax(160px,1fr) 96px 104px 96px 40px'}
function headerRow(){
  const r=cell('row','batch-row batch-head');r.style.gridTemplateColumns=columns();
  const labels=[['Produto',''],[isTransfer?'Estoque na origem':'Estoque atual','num'],['Quantidade','num']];
  if(isEntry)labels.push(['Preço da embalagem','num']);
  labels.push(['Estoque após','num'],['','']);
  labels.forEach(([t,c])=>{const h=cell('columnheader',c);if(t)h.textContent=t;else{const s=document.createElement('span');s.className='sr-only';s.textContent='Remover';h.append(s)}r.append(h)});
  return r;
}
function lineRow(line,i){
  const r=cell('row','batch-row');r.style.gridTemplateColumns=columns();r.dataset.key=line.key;
  const l=skuLabel(line);
  const name=cell('cell','batch-name');const strong=document.createElement('strong');strong.textContent=l.item;const meta=document.createElement('span');meta.textContent=l.meta;name.append(strong,meta);
  const bal=cell('cell','num');bal.dataset.role='balance';
  const q=cell('cell','num');const qi=document.createElement('input');qi.inputMode='decimal';qi.value=line.qty;qi.setAttribute('aria-label','Quantidade de '+l.item);qi.className='batch-qty';
  qi.addEventListener('input',()=>{line.qty=qi.value;r.classList.remove('has-error');refreshComputed()});q.append(qi);
  r.append(name,bal,q);
  if(isEntry){const p=cell('cell','num');const pi=document.createElement('input');pi.inputMode='decimal';pi.value=line.price;pi.setAttribute('aria-label','Preço da embalagem de '+l.item);pi.addEventListener('input',()=>{line.price=pi.value;r.classList.remove('has-error')});p.append(pi);r.append(p)}
  const after=cell('cell','num batch-after');after.dataset.role='after';r.append(after);
  const rm=document.createElement('button');rm.type='button';rm.className='batch-remove secondary';rm.setAttribute('role','cell');rm.setAttribute('aria-label','Remover '+l.item);rm.textContent='×';
  rm.addEventListener('click',()=>{lines=lines.filter(x=>x.key!==line.key);renderTable();search.focus()});r.append(rm);
  return r;
}
function renderTable(){
  table.textContent='';
  if(!lines.length){
    const e=document.createElement('p');e.className='batch-empty';e.textContent='Nenhum produto no lote. Bipe um código ou busque pelo nome.';table.append(e);
  }else{
    table.append(headerRow());
    lines.forEach((ln,i)=>table.append(lineRow(ln,i)));
  }
  refreshComputed();
}
function refreshComputed(){
  lines.forEach(line=>{
    const r=table.querySelector('[data-key="'+line.key+'"]');if(!r)return;
    const bal=currentBalance(line),after=afterBalance(line);
    r.querySelector('[data-role="balance"]').textContent=bal===null?'…':fmt(bal)+' '+(line.unit||'');
    const a=r.querySelector('[data-role="after"]');
    if(after===null){a.textContent='…';a.classList.remove('is-negative')}
    else{a.textContent=fmt(after)+' '+(line.unit||'');a.classList.toggle('is-negative',!isEntry&&after<0)}
  });
  renderFacts();
}

/* ---------- Resumo ---------- */
function routeText(){
  const o=wh.selectedOptions[0]?.textContent||'',d=to.selectedOptions[0]?.textContent||'';
  if(isTransfer)return o&&d?o+' → '+d:'Escolha origem e destino';
  return o||'Escolha o depósito';
}
function renderFacts(){
  const total=lines.reduce((n,l)=>n+num(l.qty),0);
  const rows=[['Operação',verb],['Produtos',String(lines.length)],['Embalagens',fmt(total,4)],[isTransfer?'Rota':'Depósito',routeText()]];
  facts.textContent='';
  rows.forEach(([k,v])=>{const d=document.createElement('div');const dt=document.createElement('dt'),dd=document.createElement('dd');dt.textContent=k;dd.textContent=v;d.append(dt,dd);facts.append(d)});
  const n=lines.length;
  submit.textContent=n?'Confirmar '+verb.toLowerCase()+' de '+plural(n,'produto','produtos'):'Confirmar '+verb.toLowerCase();
  submit.disabled=!n||!!pending||submit.dataset.busy==='1';
  clearBtn.disabled=!n||!!pending;
}

/* ---------- Adicionar produto ---------- */
async function addProduct(r){
  hideLookup();lookupNote('','');
  const existing=lines.find(x=>x.sku_id===r.sku_id);
  if(existing){
    existing.qty=String(num(existing.qty)+1).replace('.',',');
    renderTable();
    const row=table.querySelector('[data-key="'+existing.key+'"]');
    row?.classList.add('is-flash');setTimeout(()=>row?.classList.remove('is-flash'),900);
    search.value='';search.focus();return;
  }
  const line=Object.assign({key:'l'+(++seq),content_quantity:'1',qty:'1',price:'',rows:null},r);
  line.content=line.content_quantity;
  lines.push(line);search.value='';renderTable();
  try{line.rows=await api('/api/stock/availability?sku_id='+encodeURIComponent(r.sku_id))}catch(err){line.rows=[];lookupNote('error','Não foi possível consultar o estoque de '+esc(skuLabel(r).item)+'. O servidor confere de novo ao confirmar.')}
  refreshComputed();
  const row=table.querySelector('[data-key="'+line.key+'"] .batch-qty');row?.focus();row?.select();
}
let lookupSeq=0;
async function runSearch(exactOnly){
  const q=normalizeBarcode(search.value),mine=++lookupSeq;
  if(q.length<2){hideLookup();lookupNote('','');return}
  lookupNote('loading','Procurando…');
  let rows;try{rows=await productLookup(q)}catch(e){if(mine===lookupSeq)lookupNote('error','Não foi possível consultar os produtos.');return}
  if(mine!==lookupSeq)return;
  lookupNote('','');
  const exact=rows.find(r=>normalizeBarcode(r.barcode)===q);
  if(exact&&exactOnly){await addProduct(exact);return}
  if(!rows.length){hideLookup();const canCatalog=!!document.querySelector('.nav a[href="/products"]');const href='/products?q='+encodeURIComponent(q)+(isEntry?'&return_to=%2Fentry':'');lookupNote('error','<strong>Nenhum produto encontrado</strong><div class="actions">'+(canCatalog?'<a class="button secondary" href="'+href+'">Cadastrar este produto</a>':'')+'</div>');return}
  if(exactOnly&&rows.length===1){await addProduct(rows[0]);return}
  showLookup(rows,lookup,addProduct);
}
let timer=0;
search.addEventListener('input',()=>{clearTimeout(timer);timer=setTimeout(()=>runSearch(false),300)});
search.addEventListener('keydown',e=>{if(e.key!=='Enter')return;e.preventDefault();clearTimeout(timer);runSearch(true)});
addBtn.addEventListener('click',()=>runSearch(true));

/* ---------- Depósitos ---------- */
function fillSelect(sel,list,placeholder){
  sel.textContent='';const p=document.createElement('option');p.value='';p.textContent=placeholder;sel.append(p);
  list.forEach(w=>{const o=document.createElement('option');o.value=w.id;o.textContent=w.name;sel.append(o)});
}
function syncDestination(){
  if(!isTransfer)return;
  const keep=to.value;
  fillSelect(to,warehouses.filter(w=>String(w.id)!==String(wh.value)),'Escolha o destino');
  if(keep&&String(keep)!==String(wh.value))to.value=keep;
}
wh.addEventListener('change',()=>{syncDestination();refreshComputed()});
to.addEventListener('change',renderFacts);
async function loadWarehousesForBatch(){
  try{warehouses=await api('/api/warehouses')}catch(err){note('error',uiErrorMessage(err,'Não foi possível carregar os depósitos.'));return}
  fillSelect(wh,warehouses,isTransfer?'Escolha a origem':'Escolha o depósito');
  if(warehouses.length===1&&!isTransfer)wh.value=warehouses[0].id;
  syncDestination();renderFacts();
}

/* ---------- Validação e envio ---------- */
function validate(){
  note('','');
  if(pending){note('error','Há um lançamento sem confirmação. Toque em “Conferir” antes de fazer outro.');return false}
  if(!wh.value){note('error',isTransfer?'Escolha o depósito de origem.':'Escolha o depósito.');wh.focus();return false}
  if(isTransfer){if(!to.value){note('error','Escolha o depósito de destino.');to.focus();return false}if(to.value===wh.value){note('error','O destino precisa ser diferente da origem.');return false}}
  if(!lines.length){note('error','Adicione ao menos um produto ao lote.');search.focus();return false}
  for(let i=0;i<lines.length;i++){
    const l=lines[i],row=table.querySelector('[data-key="'+l.key+'"]'),label=skuLabel(l).item;
    const fail=msg=>{row?.classList.add('has-error');note('error','Item '+(i+1)+', '+label+': '+msg);return false};
    if(!decimalOK(l.qty)||num(l.qty)<=0)return fail('digite uma quantidade maior que zero, com até 4 casas decimais.');
    if(isEntry&&(!decimalOK(l.price)))return fail('digite o preço da embalagem.');
    if(!isEntry){const bal=currentBalance(l);if(bal!==null&&num(l.qty)*(num(l.content)||1)>bal)return fail('a quantidade é maior que o estoque ('+fmt(bal)+' '+(l.unit||'')+').')}
  }
  return true;
}
function buildBody(){
  const body={operation_id:newOperationID(),quantity_semantics:'presentation',document_number:doc.value,notes:notes.value,
    lines:lines.map(l=>{const o={sku_id:l.sku_id,quantity:requestDecimal(l.qty)};if(isEntry)o.unit_cost=requestDecimal(l.price);return o})};
  if(isTransfer){body.warehouse_from_id=+wh.value;body.warehouse_to_id=+to.value}else body.warehouse_id=+wh.value;
  return body;
}

/* Um único lançamento sem confirmação por pessoa, compartilhado com o assistente do celular. */
const store=()=>window.OfflineStore?new OfflineStore():null;
async function loadPendingRecord(){
  const s=store();if(!s)return null;
  try{const r=await s.getPending('movement');if(r&&r.payload&&typeof r.payload.operation_id==='string')return{url:r.endpoint,body:r.payload,summary:r.payload.__summary||''}}catch(e){}
  return null;
}
async function claim(p){
  const s=store();if(!s)return true;
  try{const res=await s.claimPending('movement',p.body.operation_id,p.url,p.body);return !!res.claimed}catch(e){return true}
}
async function dropPending(){
  const id=pending?.body?.operation_id;
  if(id&&store()){try{await store().clearPending('movement',id)}catch(e){}}
  pending=null;pendingState='';pendingBox.hidden=true;retryBtn.hidden=true;discardBtn.hidden=true;lock(false);renderFacts();
}
function lock(on){[wh,to,search,addBtn,doc,notes].forEach(el=>{el.disabled=on});table.querySelectorAll('input,button').forEach(el=>{el.disabled=on})}
function showPending(retryable,discardable){
  disarmDiscard(discardBtn);pendingBox.hidden=false;
  const name=pending.url.includes('/entry')?'Entrada':pending.url.includes('/exit')?'Saída':'Transferência';
  const n=Array.isArray(pending.body.lines)?pending.body.lines.length:1;
  pendingText.textContent=name+' sem confirmação · '+plural(n,'produto','produtos')+'. Confira se foi registrada antes de fazer outro lançamento.';
  retryBtn.hidden=!retryable;discardBtn.hidden=!discardable;lock(true);renderFacts();
}
function summaryOfLaunch(){
  const names=lines.slice(0,2).map(l=>skuLabel(l).item).join(', ');
  return{time:new Date().toLocaleTimeString('pt-BR',{hour:'2-digit',minute:'2-digit'}),verb,count:lines.length,names:names+(lines.length>2?' e mais '+(lines.length-2):'')};
}
function renderRecent(){
  recentBox.hidden=!launches.length;recent.textContent='';
  launches.slice(0,5).forEach(x=>{
    const r=document.createElement('div');r.className='batch-recent-row';
    [['time',x.time],['kind',x.verb],['names',x.names],['count',plural(x.count,'produto','produtos')]].forEach(([c,t])=>{const s=document.createElement('span');s.className=c;s.textContent=t;r.append(s)});
    recent.append(r);
  });
}
function reset(){lines=[];doc.value='';notes.value='';search.value='';hideLookup();lookupNote('','');renderTable()}
async function confirmed(text){
  launches.unshift(summaryOfLaunch());
  await dropPending();
  reset();renderRecent();note('success',text);search.focus();
}
async function verify(){
  if(!pending)return'none';
  note('loading','Conferindo o lançamento…');
  /* checkOperation (app.js): só consulta, com prazo e poucas tentativas; nunca reenvia. */
  const res=await checkOperation(pending.body.operation_id);
  if(res.state==='confirmed'){await confirmed('O lançamento já estava registrado.');return'confirmed'}
  pendingState=res.state;
  if(res.state==='missing'){showPending(true,true);note('error','O lançamento não chegou ao sistema. Envie de novo ou descarte.');return'missing'}
  showPending(false,true);note('error',uncheckedPendingMessage(res.err));return'unknown';
}
function markLineFromMessage(msg){
  const m=/^Item (\d+)/.exec(msg||'');if(!m)return;
  const l=lines[+m[1]-1];if(l)table.querySelector('[data-key="'+l.key+'"]')?.classList.add('has-error');
}
async function send(p,again){
  note('loading',again?'Enviando de novo…':'Enviando…');
  try{
    await api(p.url,{method:'POST',body:JSON.stringify(p.body),timeoutMs:movementSendTimeoutMs});
    await confirmed(verb+' de '+plural(p.body.lines?p.body.lines.length:1,'produto','produtos')+' registrada.');
  }catch(err){
    if(err.status===409&&String(err.message||'').toLowerCase().includes('operation_id conflict')){pendingState='conflict';showPending(false,true);note('error',operationConflictMessage);return}
    if(err.status>=400&&err.status<500){await dropPending();note('error',uiErrorMessage(err,'Não foi possível concluir o lançamento.'));markLineFromMessage(err.message);refreshAvailability();return}
    pendingState='';showPending(true,false);note('error','Não foi possível saber se o lançamento foi registrado. Toque em “Conferir” antes de fazer outro.');
  }
}
async function refreshAvailability(){
  await Promise.all(lines.map(async l=>{try{l.rows=await api('/api/stock/availability?sku_id='+encodeURIComponent(l.sku_id))}catch(e){}}));
  refreshComputed();
}
submit.addEventListener('click',async()=>{
  if(submit.dataset.busy==='1')return;
  if(!validate())return;
  submit.dataset.busy='1';submit.disabled=true;
  const body=buildBody(),p={url:endpoint,body};
  pending=p;
  if(!await claim(p)){pending=await loadPendingRecord();if(pending)showPending(false,false);note('error','Há um lançamento sem confirmação. Toque em “Conferir” antes de fazer outro.');submit.dataset.busy='';return}
  try{await send(p,false)}finally{submit.dataset.busy='';renderFacts()}
});
checkBtn.addEventListener('click',()=>runLocked(checkBtn,verify));
retryBtn.addEventListener('click',()=>runLocked(retryBtn,async()=>{const s=await verify();if(s!=='missing'||!pending)return;await send(pending,true)}));
discardBtn.addEventListener('click',async()=>{if(pendingState!=='missing'&&!armDiscard(discardBtn)){note('error',uncheckedDiscardWarning);return}disarmDiscard(discardBtn);await dropPending();note('success','Lançamento descartado. Você já pode fazer outro.')});
clearBtn.addEventListener('click',()=>{reset();note('','')});

/* ---------- Início ---------- */
renderTable();renderRecent();
loadWarehousesForBatch();
loadPendingRecord().then(p=>{if(p){pending=p;showPending(false,false);note('error','Há um lançamento sem confirmação. Toque em “Conferir” antes de fazer outro.')}});
const q=new URLSearchParams(location.search);
if(q.get('sku_id')){addProduct({sku_id:+q.get('sku_id'),item:q.get('item')||'',brand:q.get('brand')||'',barcode:q.get('barcode')||'',unit:q.get('unit')||'',content_quantity:q.get('content_quantity')||'1'})}
if(q.get('warehouse_id')&&!isTransfer){const t=setInterval(()=>{if(wh.options.length>1){wh.value=q.get('warehouse_id');clearInterval(t);refreshComputed()}},100);setTimeout(()=>clearInterval(t),3000)}
if(q.get('item_id')){api('/api/stock/detail/'+encodeURIComponent(q.get('item_id'))).then(d=>{
  const rows=(d.presentations||[]).map(x=>({sku_id:x.sku_id,item:d.name,brand:x.sku||x.brand||'',barcode:x.barcode||'',unit:d.unit,content_quantity:x.content_quantity||'1'}));
  search.value=d.name;
  if(rows.length===1){addProduct(rows[0])}else if(rows.length>1){showLookup(rows,lookup,addProduct);lookupNote('','Escolha a embalagem do produto.')}
}).catch(err=>lookupNote('error',esc(uiErrorMessage(err,'Não foi possível carregar o produto.'))))}
search.focus();
})();
