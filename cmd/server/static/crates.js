(()=>{
'use strict';
const $=id=>document.getElementById(id);
if(!$('crateReceiveForm'))return;
const weightsBox=$('crateWeights'),countInput=$('crateCount');
let selected=null,currentCrate=null,searchTimer=0;
// Cada formulário mostra o aviso logo abaixo do próprio botão; um aviso no topo
// da página fica fora da vista no celular e o botão parece não responder.
// Rola até o aviso só quando ele é do formulário que a pessoa acabou de enviar,
// nunca no carregamento da página. (O botão fica desabilitado durante o envio e
// perde o foco, por isso não dá para usar o foco.)
let lastSubmitted=null;
document.addEventListener('submit',e=>{lastSubmitted=e.target},true);
function noteIn(id,type,message){
 const el=$(id);if(!el)return;
 setFeedback(el,type,message);
 if(type&&!el.hidden&&lastSubmitted&&el.closest('form')===lastSubmitted)el.scrollIntoView({block:'nearest'});
}
function parseKg(raw){let value=String(raw||'').trim().replace(',','.');return /^\d{1,9}(\.\d{1,3})?$/.test(value)?Number(value):NaN;}
function canonical(raw){return String(raw||'').trim().replace(',','.');}
function pendingKey(kind){return 'totalfood:crate:'+kind;}
function getPending(kind){try{return JSON.parse(sessionStorage.getItem(pendingKey(kind))||'null')}catch{return null}}
function setPending(kind,body){sessionStorage.setItem(pendingKey(kind),JSON.stringify(body));}
function clearPending(kind){sessionStorage.removeItem(pendingKey(kind));}
function refreshWeights(){
 const n=Math.max(1,Math.min(50,Number(countInput.value)||1));
 const previous=Array.from(weightsBox.querySelectorAll('input')).map(x=>x.value);
 weightsBox.replaceChildren();
 for(let i=0;i<n;i++){
  const row=document.createElement('div');row.className='crate-weight-row';
  const label=document.createElement('label');label.textContent='Caixa '+(i+1)+'/'+n;
  const input=document.createElement('input');input.inputMode='decimal';input.placeholder='0,000';
  input.value=previous[i]||'';input.required=true;input.dataset.crateWeight='1';
  input.setAttribute('aria-label','Peso líquido da caixa '+(i+1)+' de '+n+' em quilogramas');
  input.addEventListener('input',updateTotal);label.append(input);row.append(label);weightsBox.append(row);
 }
 updateTotal();
}
function updateTotal(){
 const values=Array.from(weightsBox.querySelectorAll('input')).map(x=>parseKg(x.value));
 const valid=values.every(v=>Number.isFinite(v)&&v>0);
 $('crateTotal').textContent=valid?values.reduce((a,b)=>a+b,0).toLocaleString('pt-BR',{minimumFractionDigits:3,maximumFractionDigits:3})+' kg':'Preencha todos os pesos';
}
countInput.addEventListener('input',refreshWeights);refreshWeights();
$('crateSearch').addEventListener('input',()=>{
 selected=null;$('crateSku').value='';$('crateSelected').textContent='Escolha o produto na lista. Para hortifruti, a unidade base deve ser KG.';
 clearTimeout(searchTimer);
 const q=$('crateSearch').value.trim();
 if(q.length<2){$('crateProductLookup').hidden=true;return}
 searchTimer=setTimeout(async()=>{
  try{
   // O servidor filtra KG antes do limite de 10 resultados; só sem nenhum em KG
   // buscamos sem filtro, para explicar o que foi encontrado.
   const rows=await api('/api/products/lookup?unit=KG&q='+encodeURIComponent(normalizeBarcode(q)));
   if($('crateSearch').value.trim()!==q)return; // resposta de uma busca anterior
   if(!rows.length){const found=await productLookup(q);if($('crateSearch').value.trim()!==q)return;lookupHint(found);return}
   showLookup(rows,$('crateProductLookup'),r=>{
    selected=r;$('crateSku').value=r.sku_id;
    $('crateSearch').value=r.item;
    $('crateSelected').textContent='Produto selecionado: '+r.item+(r.brand?' · '+r.brand:'')+' · '+r.unit;
   });
  }catch(e){const box=$('crateProductLookup'),p=document.createElement('p');p.className='hint';p.textContent=uiErrorMessage(e,'Falha na busca de produtos.');box.replaceChildren(p);box.hidden=false}
 },250);
});
// Caixas só aceitam produto com unidade base KG. Sem esse aviso a lista some e
// o envio só diz "Selecione um produto da lista", sem dizer por quê.
function lookupHint(found){
 const box=$('crateProductLookup');box.replaceChildren();
 const p=document.createElement('p');p.className='hint';
 if(!found.length){p.textContent='Nenhum produto ativo encontrado com esse nome. Confira a grafia ou o cadastro.';}
 else{
  const names=[...new Set(found.map(r=>r.item+' ('+r.unit+')'))];
  p.textContent='Nenhum produto em KG com esse nome. Encontrado fora de KG: '+names.slice(0,3).join(', ')+(names.length>3?' e outros':'')+'. Caixas exigem produto com unidade base KG; ajuste o cadastro no Catálogo.';
 }
 box.append(p);box.hidden=false;
}
async function warehouses(){
 try{
  const rows=await api('/api/warehouses');for(const id of ['crateWarehouse','crateTransferWarehouse']){const s=$(id);if(!s)continue;s.replaceChildren();for(const w of rows){const o=document.createElement('option');o.value=w.id;o.textContent=w.name;s.append(o);}}
  const f=$('crateActiveWarehouse');if(f)for(const w of rows){const o=document.createElement('option');o.value=w.id;o.textContent=w.name;f.append(o);}
 }catch(e){noteIn('crateReceiveStatus','error',uiErrorMessage(e,'Não foi possível carregar os depósitos.'))}
}
function displayCrates(rows){
 const box=$('crateReceiveResult');box.replaceChildren();
 const title=document.createElement('h3');title.textContent='Etiquetas criadas';box.append(title);
 for(const c of rows){
  const row=document.createElement('div');row.className='crate-result';
  const strong=document.createElement('strong');strong.textContent=c.code+' · '+c.position+'/'+c.total;
  const t=document.createTextNode(' · '+displayDecimal(c.weight)+' kg');
  const link=document.createElement('a');link.href='/crates?crate='+encodeURIComponent(c.code);link.textContent='Consultar';
  row.append(strong,t,link);box.append(row);
 }
}
async function sendReceipt(body){
 const out=await api('/api/crates/receive',{method:'POST',body:JSON.stringify(body),timeoutMs:30000});
 clearPending('receive');displayCrates(out.crates||[]);noteIn('crateReceiveStatus','success','Entrada registrada e etiquetas adicionadas à fila.');await jobs();activeCrates();
}
$('crateReceiveForm').addEventListener('submit',async e=>{
 e.preventDefault();
 await runLocked($('crateReceiveSubmit'),async()=>{
  let body=getPending('receive');
  if(!body){
   const weights=Array.from(weightsBox.querySelectorAll('input')).map(x=>canonical(x.value));
   if(!selected||!selected.sku_id){noteIn('crateReceiveStatus','error','Selecione um produto da lista.');return}
   if(weights.some(w=>!Number.isFinite(parseKg(w))||parseKg(w)<=0)){noteIn('crateReceiveStatus','error','Informe um peso líquido válido para cada caixa.');return}
   const cost=canonical($('crateCost').value);
   if(!/^\d+(\.\d{1,4})?$/.test(cost)){noteIn('crateReceiveStatus','error','Digite um custo válido.');return}
   body={operation_id:newOperationID(),sku_id:selected.sku_id,warehouse_id:Number($('crateWarehouse').value),
    unit_cost:cost,document_number:$('crateDocument').value.trim(),supplier_name:$('crateSupplier').value.trim(),
    origin_lot:$('crateOriginLot').value.trim(),weights};
   setPending('receive',body);
  }
  noteIn('crateReceiveStatus','loading','Registrando as caixas…');
  try{await sendReceipt(body);}
  catch(err){
   if(definitiveWriteError(err)){clearPending('receive');noteIn('crateReceiveStatus','error',uiErrorMessage(err,'Entrada recusada. Confira os campos.'));}
   else{noteIn('crateReceiveStatus','error','Não foi possível confirmar a entrada. O mesmo lançamento será reenviado ao tocar novamente. Não crie outro.');}
  }
 });
});
function idFromCode(code){
 const m=/^(?:CX-)?0*([1-9]\d*)$/i.exec(String(code||'').trim());
 return m?Number(m[1]):null;
}
async function loadCrate(id){
 const c=await api('/api/crates/'+id);currentCrate=c;
 $('crateDetail').hidden=false;
 $('crateCurrent').textContent=c.code+' · '+c.product+' · Peso atual '+displayDecimal(c.weight)+' kg · NF '+(c.document||'não informada');
 $('crateWeighWeight').value=String(c.weight).replace('.',',');
 $('crateWeighNotes').value='';
 if($('crateTransferWarehouse'))$('crateTransferWarehouse').value='';
}
initScanner('scanCrate',async code=>{
 const id=idFromCode(code);
 if(!id){noteIn('crateFindStatus','error','O QR Code não contém um código de caixa válido.');return}
 $('crateFind').value='CX-'+String(id).padStart(6,'0');
 try{await loadCrate(id);noteIn('crateFindStatus','success','Caixa identificada pelo QR Code.');}
 catch(err){noteIn('crateFindStatus','error',uiErrorMessage(err,'Não foi possível consultar a caixa.'))}
},qrFormats);
$('crateFindForm').addEventListener('submit',async e=>{
 e.preventDefault();const id=idFromCode($('crateFind').value);
 if(!id){noteIn('crateFindStatus','error','Digite um código no formato CX-000123.');return}
 try{await loadCrate(id);noteIn('crateFindStatus','','');}catch(err){$('crateDetail').hidden=true;noteIn('crateFindStatus','error',uiErrorMessage(err,'Caixa não encontrada.'));}
});
$('crateWeighForm').addEventListener('submit',async e=>{
 e.preventDefault();if(!currentCrate)return;
 await runLocked($('crateWeighSubmit'),async()=>{
  const key='weigh:'+currentCrate.id;let body=getPending(key);
  if(!body){
   const weight=canonical($('crateWeighWeight').value);
   if(!Number.isFinite(parseKg(weight))||parseKg(weight)<0){noteIn('crateWeighStatus','error','Informe um peso válido.');return}
   body={operation_id:newOperationID(),weight,notes:$('crateWeighNotes').value.trim()};
   setPending(key,body);
  }
  try{
   const c=await api('/api/crates/'+currentCrate.id+'/weigh',{method:'POST',body:JSON.stringify(body),timeoutMs:30000});
   clearPending(key);currentCrate=c;
   $('crateCurrent').textContent=c.code+' · '+c.product+' · Peso atual '+displayDecimal(c.weight)+' kg';
   noteIn('crateWeighStatus','success',c.status==='EMPTY'?'Pesagem registrada. A caixa ficou vazia e não precisa de etiqueta nova.':'Pesagem registrada. Nova etiqueta adicionada à fila.');await jobs();activeCrates();
  }catch(err){
   if(definitiveWriteError(err)){clearPending(key);noteIn('crateWeighStatus','error',uiErrorMessage(err,'Pesagem recusada.'));}
   else noteIn('crateWeighStatus','error','Confirmação não recebida. Reenvie a mesma pesagem, sem alterá-la.');
  }
 });
});
async function jobs(){
 try{
  const data=await api('/api/print/jobs');const box=$('cratePrintJobs');box.replaceChildren();
  if(!data.jobs?.length){box.textContent='Não há trabalhos de impressão.';return}
  for(const job of data.jobs){
   const row=document.createElement('div');row.className='crate-job';
   const label=document.createElement('span');
   const names={PENDING:'Pendente',CLAIMED:'Reservada',SENT:'Enviada ao USB',UNCERTAIN:'Confirmação incerta',FAILED:'Falha',CANCELLED:'Substituída'};
   label.textContent=job.crate_code+' · '+job.product+' · rev '+job.revision+' · '+(names[job.status]||job.status);
   row.append(label);
   if(['UNCERTAIN','FAILED'].includes(job.status)){
    const button=document.createElement('button');button.className='secondary';button.textContent='Reenviar';
    button.onclick=()=>runLocked(button,async()=>{
     if(!confirm('A impressora pode já ter impresso esta etiqueta. Deseja reenviar?'))return;
     try{await api('/api/print/jobs/'+job.id+'/retry',{method:'POST',body:'{}'});await jobs();}
     catch(err){noteIn('crateQueueStatus','error',uiErrorMessage(err,'Não foi possível reenviar a etiqueta.'))}
    });row.append(button);
   }box.append(row);
  }
 }catch(err){noteIn('crateQueueStatus','error',uiErrorMessage(err,'Não foi possível consultar a fila.'))}
}
$('crateTransferForm')?.addEventListener('submit',async e=>{
 e.preventDefault();if(!currentCrate)return;
 const btn=$('crateTransferSubmit'),key='transfer:'+currentCrate.id;
 await runLocked(btn,async()=>{
  let body=getPending(key);
  if(!body){body={operation_id:newOperationID(),to_warehouse_id:Number($('crateTransferWarehouse').value)};
   if(!body.to_warehouse_id){noteIn('crateTransferStatus','error','Escolha um depósito de destino.');return;}
   setPending(key,body);
  }
  try{
   const c=await api('/api/crates/'+currentCrate.id+'/transfer',{method:'POST',body:JSON.stringify(body),timeoutMs:30000});
   clearPending(key);currentCrate=c;
   $('crateCurrent').textContent=c.code+' · '+c.product+' · '+displayDecimal(c.weight)+' kg · Depósito '+c.warehouse_id;
   noteIn('crateTransferStatus','success','Caixa transferida sem alterar sua identificação.');activeCrates();
  }catch(err){
   if(definitiveWriteError(err)){clearPending(key);noteIn('crateTransferStatus','error',uiErrorMessage(err,'Transferência recusada.'));}
   else noteIn('crateTransferStatus','error','Confirmação não recebida. Reenvie a mesma transferência sem modificá-la.');
  }
 });
});
// Caixas ativas: o que existe com peso, por depósito. Tocar em uma caixa abre a
// pesagem/transferência dela, sem precisar digitar o código.
async function activeCrates(){
 const box=$('crateActiveList');if(!box)return;
 const wh=$('crateActiveWarehouse').value;
 let rows;
 try{rows=await api('/api/crates'+(wh?'?warehouse_id='+encodeURIComponent(wh):''));}
 catch(err){noteIn('crateActiveStatus','error',uiErrorMessage(err,'Não foi possível carregar as caixas ativas.'));return}
 noteIn('crateActiveStatus','','');box.replaceChildren();
 const total=rows.reduce((s,c)=>s+(Number(c.weight)||0),0);
 $('crateActiveSummary').textContent=rows.length?rows.length+(rows.length===1?' caixa, ':' caixas, ')+displayDecimal(total.toFixed(3))+' kg':'Nenhuma caixa ativa'+(wh?' neste depósito.':'.');
 for(const c of rows){
  const b=document.createElement('button');b.type='button';b.className='crate-active-row';
  const a=document.createElement('span'),name=document.createElement('strong'),meta=document.createElement('small'),w=document.createElement('strong');
  name.textContent=c.product;meta.textContent=c.code+', '+c.warehouse+', recebida em '+c.received+(c.total>1?', caixa '+c.position+' de '+c.total:'');
  a.append(name,meta);w.textContent=displayDecimal(c.weight)+' kg';b.append(a,w);
  b.addEventListener('click',async()=>{
   $('crateFind').value=c.code;
   try{await loadCrate(c.id);noteIn('crateFindStatus','','');$('crateDetail').scrollIntoView({block:'center'});}
   catch(err){noteIn('crateFindStatus','error',uiErrorMessage(err,'Não foi possível consultar a caixa.'))}
  });
  box.append(b);
 }
}
$('crateActiveWarehouse')?.addEventListener('change',()=>activeCrates());
$('crateQueueRefresh').addEventListener('click',()=>jobs());
warehouses();jobs();activeCrates();
const requested=new URLSearchParams(location.search).get('crate');
if(requested){const id=idFromCode(requested);if(id){$('crateFind').value=requested;loadCrate(id).catch(()=>{})}}
})();
