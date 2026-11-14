(() => {
'use strict';
const root=document.getElementById('materialsRoot');if(!root)return;
const $=id=>document.getElementById(id);
const canWrite=root.dataset.canWrite==='1',canFulfill=root.dataset.canFulfill==='1',canReceive=root.dataset.canReceive==='1';
const status=$('materialStatus'),pendingBox=$('materialPending'),list=$('materialList'),detail=$('materialDetail'),detailContent=$('materialDetailContent');
const serviceLabel={cafe:'Café da manhã',almoco:'Almoço',jantar:'Jantar'};
let draft=[],current=0,busy=false,pending=null,searchSequence=0,searchTimer=0;
const pendingKey='material-request:pending:'+document.querySelector('meta[name="current-user-id"]')?.content;
const offline=window.OfflineStore?new OfflineStore():null;
const day=s=>s?s.split('-').reverse().join('/'):'—';
const number=s=>new Intl.NumberFormat('pt-BR',{maximumFractionDigits:3}).format(Number(s)||0);
const stamp=s=>s?new Date(s).toLocaleString('pt-BR'):'—';
const el=(tag,text,cls)=>{const e=document.createElement(tag);if(text!==undefined&&text!==null)e.textContent=String(text);if(cls)e.className=cls;return e};
const field=(label,input)=>{const e=el('label');e.append(el('span',label),input);return e};
const btn=(text,click,secondary=true)=>{const b=el('button',text,secondary?'secondary':'');b.type='button';b.addEventListener('click',click);return b};
function note(type,msg){setFeedback(status,type,msg)}
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
async function run(url,body,message){
 await pendingReady;
 if(busy||pending){note('error','Há uma operação sem confirmação. Toque em “Enviar de novo” antes de continuar.');return false}
 const fresh={url,body,operation_id:body.operation_id||newOperationID()};
 if(offline)try{
  const claimed=await offline.claimPending('material-request',fresh.operation_id,url,body);
  if(!claimed.claimed){
   pending={url:claimed.record.endpoint,body:claimed.record.payload,operation_id:claimed.record.operation_id};
   sessionStorage.setItem(pendingKey,JSON.stringify(pending));showPending();
   note('error','Existe uma operação pendente neste dispositivo. Confira antes de continuar.');
   return false;
  }
 }catch(e){}
 pending=fresh;sessionStorage.setItem(pendingKey,JSON.stringify(pending));showPending();
 return sendPending(message);
}
async function sendPending(message){
 await pendingReady;
 if(!pending||busy)return false;
 busy=true;showPending();note('loading',message||'Enviando operação…');
 const sent=pending;
 try{
  await api(sent.url,{method:'POST',body:JSON.stringify(sent.body),timeoutMs:movementSendTimeoutMs});
  await clearPending(sent);
  if(sent.url==='/api/material-requests'&&canWrite){
   draft=[];
   $('materialDraft').textContent='';
   $('materialCreateForm').reset();
   $('materialDeliveryDate').value=todayProductionDate();
   $('materialProductionDate').value=todayProductionDate();
  }
  note('success','Operação registrada.');await refresh();
  return true;
 }catch(e){
  if(e.status>=400&&e.status<500){
   await clearPending(sent);
   note('error',uiErrorMessage(e,'Confira os dados e tente novamente.'));
  }else{
   note('error','A resposta não foi confirmada. Reenvie a mesma operação para conferir sem duplicar.');
  }
  return false;
 }finally{busy=false;showPending()}
}
$('materialRetry').addEventListener('click',()=>sendPending('Conferindo a operação anterior…'));
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
   const state=el('span',r.status.replaceAll('_',' '),'material-tag');
   b.append(h,sub,state);b.setAttribute('aria-current',String(current===r.id));list.append(b);
  });
  if(current)await openReq(current,false);
 }catch(e){note('error',uiErrorMessage(e,'Não foi possível atualizar as requisições.'))}
}
async function openReq(id,scroll=true){
 try{
  const r=await api('/api/material-requests/'+id);
  current=id;detail.hidden=false;renderDetail(r);
  list.querySelectorAll('button[aria-current]').forEach(b=>b.setAttribute('aria-current',String(b.firstChild?.textContent?.startsWith('#'+id+' · '))));
  if(scroll)detail.scrollIntoView({block:'start',behavior:'auto'});
 }catch(e){note('error',uiErrorMessage(e,'Não foi possível abrir a requisição.'))}
}
$('materialRefresh').addEventListener('click',refresh);
$('materialClose').addEventListener('click',()=>{current=0;detail.hidden=true;list.querySelectorAll('button[aria-current]').forEach(b=>b.setAttribute('aria-current','false'))});
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
  if(!options.length){wrap.append(el('p','Não há estoque disponível deste material.','hint'));return}
  options.forEach(o=>{const opt=el('option',o.brand+' · '+o.sku+' · '+o.warehouse+' (livre '+number(o.available)+' / físico '+number(o.stock)+' '+line.unit+')');opt.value=o.sku_id+':'+o.warehouse_id;skuSelect.append(opt)});
  const qty=numericInput((Number(line.quantity)-Number(line.reserved)-Number(line.delivered)).toFixed(3));
  box.append(field('Apresentação e depósito',skuSelect),field('Separar ('+line.unit+')',qty));
  const actions=actionRow();
  actions.append(btn('Confirmar separação',async()=>{
   if(!validQuantity(qty.value)){note('error','Informe quantidade maior que zero, com até 3 casas decimais.');return}
   const [sku_id,warehouse_id]=skuSelect.value.split(':').map(Number);
   await run('/api/material-requests/'+current+'/reserve',{operation_id:newOperationID(),line_id:line.id,sku_id,warehouse_id,quantity:requestDecimal(qty.value)},'Reservando material…');
  },false),btn('Fechar',()=>{wrap.textContent=''}));
  wrap.append(box,actions);
 }catch(e){note('error',uiErrorMessage(e,'Não foi possível consultar o estoque.'))}
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
 if(canWrite&&!r.deliveries.length&&r.status!=='CANCELADA'){
  const actions=actionRow();
  actions.append(btn('Cancelar requisição',async()=>{
   if(!window.confirm('Cancelar esta requisição e liberar as reservas?'))return;
   await run('/api/material-requests/'+r.id+'/cancel',{},'Cancelando requisição…');
  }));
  detailContent.append(actions);
 }
 detailContent.append(el('h3','Materiais'));
 r.lines.forEach(line=>{
  const card=el('article',null,'material-line-details');
  card.append(el('strong',line.item),el('p','Solicitado '+number(line.quantity)+' '+line.unit+' · Separado '+number(line.reserved)+' · Entregue '+number(line.delivered),'material-small'));
  const available=Number(line.quantity)-Number(line.reserved)-Number(line.delivered);
  if(canFulfill&&available>0.0001&&r.status!=='CANCELADA'&&r.status!=='RECEBIDA'){
   const wrap=el('div');card.append(btn('Separar material',()=>chooseReserve(line,wrap)));card.append(wrap);
  }
  detailContent.append(card);
 });
 if(r.reservations.length){
  detailContent.append(el('h3','Separações'));
  r.reservations.forEach(res=>{
   const card=el('article',null,'material-reservation');
   const line=r.lines.find(x=>x.id===res.line_id);
   card.append(el('strong',(line?.item||'Material')+' · '+res.brand),el('p',res.sku+' · '+res.warehouse,'material-small'),
     el('p','Reservado '+number(res.quantity)+' · Saldo separado '+number(res.remaining)+' '+(line?.unit||'')+' · '+res.status,'material-small'));
   if(canFulfill&&res.status==='ATIVA'&&Number(res.remaining)>0){
    const input=numericInput(res.remaining);
    const actions=actionRow();
    actions.append(btn('Confirmar entrega',async()=>{
     if(!validQuantity(input.value)){note('error','Quantidade inválida.');return}
     await run('/api/material-requests/'+r.id+'/deliver',{operation_id:newOperationID(),reservation_id:res.id,quantity:requestDecimal(input.value)},'Registrando a saída física…');
    },false),btn('Liberar reserva',async()=>{
     if(!window.confirm('Liberar o saldo não entregue desta separação?'))return;
     await run('/api/material-requests/'+r.id+'/reservations/'+res.id+'/release',{},'Liberando reserva…');
    }));
    card.append(field('Quantidade a entregar',input),actions);
   }
   detailContent.append(card);
  });
 }
 if(r.deliveries.length){
  detailContent.append(el('h3','Entregas e recebimento'));
  r.deliveries.forEach(d=>{
   const card=el('article',null,'material-delivery');
   card.append(el('strong','Entrega #'+d.id+' · '+number(d.quantity)+' unidades-base'),
     el('p','Saída #'+d.movement_id+' · '+stamp(d.delivered_at)+' · '+d.delivered_by,'material-small'),
     el('p',d.received?'Recebimento confirmado por '+d.received_by:'Aguardando recebimento','material-small'));
   if(canReceive&&!d.received){
    card.append(btn('Confirmar recebimento',async()=>{
     if(!window.confirm('Você recebeu o material desta entrega?'))return;
     await run('/api/material-requests/'+r.id+'/deliveries/'+d.id+'/receive',{},'Confirmando recebimento…');
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
  }catch(e){if(seq===searchSequence)note('error','Não foi possível buscar materiais.')}
 }
 search.addEventListener('input',()=>{clearTimeout(searchTimer);searchTimer=setTimeout(()=>lookup(search.value),250)});
 form.addEventListener('submit',async e=>{
  e.preventDefault();
  if(!form.checkValidity()){form.reportValidity();return}
  if(!draft.length){note('error','Adicione pelo menos um material.');return}
  if(draft.some(x=>!validQuantity(x.quantity))){note('error','Informe todas as quantidades com até 3 casas decimais.');return}
  const body={operation_id:newOperationID(),unit_name:$('materialUnit').value.trim(),
   delivery_date:$('materialDeliveryDate').value,production_date:$('materialProductionDate').value,
   service:$('materialService').value,notes:$('materialNotes').value.trim(),
   lines:draft.map(x=>({item_id:x.id,quantity:requestDecimal(x.quantity)}))};
  await run('/api/material-requests',body,'Criando requisição…');
 });
}
refresh();
})();