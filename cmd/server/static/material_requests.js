(() => {
'use strict';
const root=document.getElementById('materialsRoot');if(!root)return;
const $=id=>document.getElementById(id);
const canWrite=root.dataset.canWrite==='1',canFulfill=root.dataset.canFulfill==='1',canReceive=root.dataset.canReceive==='1';
const status=$('materialStatus'),pendingBox=$('materialPending'),list=$('materialList'),detail=$('materialDetail'),detailContent=$('materialDetailContent');
const serviceLabel={cafe:'Café da manhã',almoco:'Almoço',lanche:'Lanche',jantar:'Jantar'};
const statusText=s=>s==='RECEBIDA_COM_DIVERGENCIA'?'RECEBIDA COM DIVERGÊNCIA':String(s||'').replaceAll('_',' ');
let draft=[],current=0,busy=false,pending=null,searchSequence=0;
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
const filters=$('materialFilters'), filterFields=filters.elements;
const listCount=$('materialListCount'), listOrder=$('materialListOrder');
const clearFilters=$('materialClearFilters');
let listSequence=0,listTimer=0,listPage=1,detailSequence=0;
function listParameters(){
 const params=new URLSearchParams();
 for(const [key,value] of new FormData(filters)){
  if(value.trim())params.set(key,value.trim());
 }
 params.set('page',String(listPage));
 return params;
}
function showListFilters(params){
 const chips=$('materialActiveFilters');
 const label=name=>filterFields.namedItem(name).selectedOptions[0]?.textContent;
 const from=params.get('from'),to=params.get('to');
 const dates=[from&&'de '+day(from),to&&'até '+day(to)].filter(Boolean).join(' ');
 const names=[
  params.has('q')&&'Busca: '+params.get('q'),
  params.has('unit')&&'Unidade: '+params.get('unit'),
  params.has('status')&&'Situação: '+label('status'),
  params.has('service')&&'Serviço: '+label('service'),
  dates&&label('date_field')+' '+dates
 ].filter(Boolean);
 chips.replaceChildren(...names.map(name=>el('li',name)));
 chips.hidden=!names.length;
 clearFilters.hidden=!names.length&&params.get('sort')==='newest';
 listOrder.textContent=label('sort');
}
async function loadRequestList(focusCount=false){
 clearTimeout(listTimer);
 const sequence=++listSequence;
 const params=listParameters();
 const from=params.get('from'),to=params.get('to');
 const feedback=$('materialListStatus');
 showListFilters(params);
 list.setAttribute('aria-busy','true');
 list.replaceChildren();
 listCount.textContent='';
 $('materialPagination').hidden=true;
 setFeedback(feedback,'','');
 try{
  if(from&&to&&from>to){
   setFeedback(feedback,'error','A data inicial deve ser anterior ou igual à data final.');
   return;
  }
  listCount.textContent='Carregando requisições…';
  const result=await api('/api/material-requests/search?'+params);
  if(sequence!==listSequence)return;
  const unit=filterFields.namedItem('unit');
  const selected=params.get('unit')||'';
  const units=[...result.units];
  if(selected&&!units.includes(selected))units.push(selected);
  unit.replaceChildren(new Option('Todas as unidades',''),...units.map(name=>new Option(name,name)));
  unit.value=selected;
  $('materialListScope').textContent=result.scope==='own'
   ?'Escopo do seu perfil: requisições criadas por você.'
   :'Escopo do seu perfil: requisições de todas as unidades.';
  const pages=Math.max(1,Math.ceil(result.filtered/result.page_size));
  if(listPage>pages){listPage=pages;return loadRequestList(focusCount)}
  const start=(listPage-1)*result.page_size+1;
  const end=start+result.requests.length-1;
  const shown=result.requests.length?(start===end?number(start):number(start)+'–'+number(end)):'0';
  listCount.textContent='Exibindo '+shown+' de '+number(result.filtered)+' no resultado'
   +(result.filtered!==result.total?' · '+number(result.total)+' acessíveis · '+number(result.total-result.filtered)+' fora dos filtros':'');
  if(!result.requests.length){
   list.append(el('p',result.total?'Nenhuma requisição corresponde aos filtros.':'Você ainda não tem requisições disponíveis.','hint'));
  }
  for(const r of result.requests){
   const button=btn('',()=>openReq(r.id));
   const materials=r.items===1?'1 material':number(r.items)+' materiais';
   button.append(
    el('strong','#'+r.id+' · '+r.unit_name),
    el('small','Produção '+day(r.production_date)+' · Entrega '+day(r.delivery_date)),
    el('small',(serviceLabel[r.service]||r.service)+' · '+materials),
    el('span',statusText(r.status),'material-tag')
   );
   button.dataset.requestId=r.id;
   button.setAttribute('aria-current',String(current===r.id));
   list.append(button);
  }
  if(pages>1){
   $('materialPagination').hidden=false;
   $('materialPageLabel').textContent='Página '+number(listPage)+' de '+number(pages);
   $('materialPreviousPage').disabled=listPage<=1;
   $('materialNextPage').disabled=listPage>=pages;
  }
  if(focusCount)listCount.focus();
 }catch(e){
  if(sequence!==listSequence)return;
  listCount.textContent='';
  setFeedback(feedback,'error',uiErrorMessage(e,'Não foi possível atualizar as requisições.'));
 }finally{
  if(sequence===listSequence)list.setAttribute('aria-busy','false');
 }
}
function applyListFilters(){
 listPage=1;
 return loadRequestList();
}
filters.addEventListener('input',event=>{
 if(event.target.name!=='q')return;
 clearTimeout(listTimer);
 ++listSequence;
 listTimer=setTimeout(applyListFilters,250);
});
filters.addEventListener('change',event=>{
 if(event.target.name!=='q')applyListFilters();
});
filters.addEventListener('submit',event=>{
 event.preventDefault();
 applyListFilters();
});
clearFilters.addEventListener('click',()=>{
 filters.reset();
 filterFields.namedItem('q').focus();
 applyListFilters();
});
$('materialPreviousPage').addEventListener('click',()=>{listPage--;loadRequestList(true)});
$('materialNextPage').addEventListener('click',()=>{listPage++;loadRequestList(true)});
async function refresh(){
 await Promise.all([loadRequestList(),current?openReq(current,false):null]);
}
async function openReq(id,scroll=true){
 const sequence=++detailSequence;
 try{
  const r=await api('/api/material-requests/'+id);
  if(sequence!==detailSequence)return;
  if(current!==id)detailNote=null;
  current=id;detail.hidden=false;renderDetail(r);
  list.querySelectorAll('button[aria-current]').forEach(b=>b.setAttribute('aria-current',String(b.dataset.requestId===String(id))));
  if(scroll)detail.scrollIntoView({block:'start',behavior:'auto'});
 }catch(e){
  if(sequence===detailSequence)report({kind:'list'},'error',uiErrorMessage(e,'Não foi possível abrir a requisição #'+id+'.'));
 }
}
$('materialRefresh').addEventListener('click',refresh);
$('materialClose').addEventListener('click',()=>{++detailSequence;current=0;detailNote=null;detail.hidden=true;list.querySelectorAll('button[aria-current]').forEach(b=>b.setAttribute('aria-current','false'))});
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
// Busca de material por nome (criação e edição usam a mesma).
function itemPicker(search,matches,taken,pick){
 let timer=0;
 async function lookup(q){
  const seq=++searchSequence;matches.textContent='';
  if(q.trim().length<2)return;
  try{
   const items=await api('/api/material-requests/items?q='+encodeURIComponent(q.trim()));
   if(seq!==searchSequence)return;
   if(!items.length){matches.append(el('p','Material não encontrado.','hint'));return}
   items.filter(item=>!taken(item.id)).forEach(item=>{
    matches.append(btn(item.name+' · '+item.unit,()=>{search.value='';matches.textContent='';pick(item)}));
   });
  }catch(e){if(seq===searchSequence)matches.append(el('p',uiErrorMessage(e,'Não foi possível buscar materiais.'),'hint'))}
 }
 search.addEventListener('input',()=>{clearTimeout(timer);timer=setTimeout(()=>lookup(search.value),250)});
}
// Edição da requisição: o rascunho sobrevive ao redesenho do detalhe (Atualizar, outras ações).
// O servidor decide o que pode mudar; a tela só avisa o mínimo de cada item já entregue.
let editing=null;
const editable=r=>canWrite&&!r.closed_at&&!['CANCELADA','RECEBIDA','RECEBIDA_COM_DIVERGENCIA'].includes(r.status);
function startEdit(r){
 editing={request:r.id,version:r.version,unit_name:r.unit_name,delivery_date:r.delivery_date,production_date:r.production_date,service:r.service,notes:r.notes||'',
  lines:r.lines.map(l=>({item_id:l.item_id,name:l.item,unit:l.unit,quantity:String(Number(l.quantity)),delivered:Number(l.delivered),reserved:Number(l.reserved)}))};
 detailNote=null;renderDetail(lastDetail);
}
function renderEdit(r,host){
 const e=editing,where={kind:'detail',key:'edit'};
 const box=el('div',null,'material-edit');box.dataset.noteKey='edit';
 box.append(el('h3','Editar requisição #'+r.id));
 const fields=el('div',null,'material-fields');
 const unit=el('input');unit.maxLength=120;unit.value=e.unit_name;unit.addEventListener('input',()=>{e.unit_name=unit.value});
 const delivery=el('input');delivery.type='date';delivery.value=e.delivery_date;delivery.addEventListener('input',()=>{e.delivery_date=delivery.value});
 const production=el('input');production.type='date';production.value=e.production_date;production.addEventListener('input',()=>{e.production_date=production.value});
 const service=el('select');Object.entries(serviceLabel).forEach(([v,t])=>{const o=new Option(t,v);service.append(o)});service.value=e.service;service.addEventListener('change',()=>{e.service=service.value});
 const delivered=r.deliveries.length>0;
 if(delivered)production.disabled=true;
 fields.append(field('Unidade solicitante',unit),field('Entrega desejada',delivery),field('Produção em',production),field('Serviço',service));
 box.append(fields);
 if(delivered)box.append(el('p','A data de produção não muda depois da primeira entrega.','material-small'));
 const lines=el('div',null,'material-draft');
 const drawLines=()=>{
  lines.textContent='';
  e.lines.forEach(x=>{
   const row=el('div',null,'material-line'),head=el('div',null,'material-line-header');
   const remove=btn('Remover',()=>{e.lines=e.lines.filter(d=>d.item_id!==x.item_id);drawLines()});
   if(x.delivered>0)remove.disabled=true;
   head.append(el('strong',x.name+' · '+x.unit),remove);
   const inp=numericInput(x.quantity);inp.addEventListener('input',()=>{x.quantity=inp.value});
   row.append(head,field('Quantidade ('+x.unit+')',inp));
   if(x.delivered>0)row.append(el('p','Já entregue '+number(x.delivered)+' '+x.unit+': não sai do pedido e não fica abaixo disso.','material-small'));
   else if(x.reserved>0)row.append(el('p','Separado '+number(x.reserved)+' '+x.unit+'. Ao remover ou reduzir, o excedente volta ao saldo livre.','material-small'));
   lines.append(row);
  });
 };
 drawLines();
 const products=el('section',null,'material-products');products.setAttribute('aria-label','Materiais da requisição');
 const search=el('input');search.type='search';search.autocomplete='off';search.placeholder='Digite o nome do material';
 const matches=el('div',null,'material-matches');matches.setAttribute('aria-live','polite');
 itemPicker(search,matches,id=>e.lines.some(x=>x.item_id===id),item=>{e.lines.push({item_id:item.id,name:item.name,unit:item.unit,quantity:'',delivered:0,reserved:0});drawLines()});
 products.append(field('Adicionar material',search),matches,lines);
 box.append(products);
 const notes=el('textarea');notes.rows=2;notes.maxLength=1000;notes.value=e.notes;notes.addEventListener('input',()=>{e.notes=notes.value});
 box.append(field('Observação (opcional)',notes));
 const actions=actionRow();
 actions.append(btn('Salvar alterações',async()=>{
  if(!e.unit_name.trim()||!e.delivery_date||!e.production_date){report(where,'error','Informe unidade, entrega desejada e data de produção.');return}
  if(!e.lines.length){report(where,'error','A requisição precisa de pelo menos um material. Para desistir do pedido, use Cancelar.');return}
  const bad=e.lines.find(x=>!validQuantity(x.quantity));
  if(bad){report(where,'error','Informe a quantidade de '+bad.name+' maior que zero, com até 3 casas decimais.');return}
  const body={version:e.version,unit_name:e.unit_name.trim(),delivery_date:e.delivery_date,production_date:e.production_date,service:e.service,notes:e.notes.trim(),
   lines:e.lines.map(x=>({item_id:x.item_id,quantity:requestDecimal(x.quantity)}))};
  await run('/api/material-requests/'+r.id+'/edit',body,{label:'a edição da requisição #'+r.id,loading:'Salvando a requisição #'+r.id+'…',
   where:{kind:'detail',key:'edit',successKey:'request'},
   success:x=>{editing=null;return x.changed===false?'Nada mudou na requisição #'+r.id+'.':'Requisição #'+r.id+' alterada'+(x.changes&&x.changes.length?': '+x.changes.join('; ')+'.':'.')},
   failure:'Edição da requisição #'+r.id+' recusada.'});
 },false),btn('Descartar alterações',()=>{editing=null;detailNote=null;renderDetail(lastDetail)}));
 box.append(actions);
 host.append(box);
}
let lastDetail=null;
function renderDetail(r){
 lastDetail=r;
 if(editing&&editing.request!==r.id)editing=null;
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
 const topActions=actionRow();
 if(editable(r)&&!editing)topActions.append(btn('Editar requisição',()=>startEdit(r)));
 if(canWrite&&!r.deliveries.length&&r.status!=='CANCELADA'){
  const actions=topActions;
  actions.append(btn('Cancelar requisição',async()=>{
   if(!window.confirm('Cancelar esta requisição e liberar as reservas?'))return;
   await run('/api/material-requests/'+r.id+'/cancel',{},{label:'o cancelamento da requisição #'+r.id,loading:'Cancelando a requisição #'+r.id+'…',
    where:{kind:'detail',key:'request'},success:()=>'Requisição #'+r.id+' cancelada e reservas liberadas.',failure:'Cancelamento da requisição #'+r.id+' recusado.'});
  }));
 }
 if(topActions.children.length)top.append(topActions);
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
 if(editing&&editing.request===r.id){
  if(editable(r))renderEdit(r,detailContent);else editing=null;
 }
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
 itemPicker(search,matches,id=>draft.some(x=>x.id===id),item=>{draft.push({...item,quantity:''});renderDraft()});
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