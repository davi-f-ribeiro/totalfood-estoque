(async function(){
const $=id=>document.getElementById(id),list=$('productList');if(!list)return;
const form=$('productForm'),dialog=$('productDialog'),status=$('productStatus'),formStatus=$('productFormStatus'),search=$('productSearch'),parent=$('productParent'),parentSearch=$('productParentSearch'),parentLookup=$('productParentLookup'),brand=$('productBrand'),brandSearch=$('productBrandSearch'),brandLookup=$('productBrandLookup'),name=$('productName'),content=$('productContent'),barcode=$('productBarcode'),preview=$('productPreview');
let products=[],editing=null;
// Aviso da lista (sucesso, carregamento da página) e aviso do formulário (validação, envio, erro): cada um junto de quem agiu.
function msg(type,text){setFeedback(status,type,text)}
function formMsg(type,text){setFeedback(formStatus,type,text)}
function currentItem(){return parentSelect.getSelected()}
function currentBrand(){return brandSelect.getSelected()}
function previewName(){let i=currentItem(),b=currentBrand(),n=name.value.trim(),v=String(content.value||'').trim(),parts=[];if(i)parts.push(i.name);if(b)parts.push(b.name);if(n)parts.push(n);if(v&&i)parts.push(v+' '+i.unit);preview.textContent=parts.join(' · ')}
const parentSelect=createPagedSearchSelect({
 input:parentSearch,valueInput:parent,lookup:parentLookup,
 fetchPage:(q,cursor)=>api('/api/catalog/items?paged=1&q='+encodeURIComponent(q)+(cursor?'&cursor='+encodeURIComponent(cursor):'')),
 label:x=>x.name,meta:x=>[x.category,x.unit].filter(Boolean).join(' · '),
 onSelect:previewName,onClear:previewName
});
const brandSelect=createPagedSearchSelect({
 input:brandSearch,valueInput:brand,lookup:brandLookup,
 fetchPage:(q,cursor)=>api('/api/catalog/brands?paged=1&q='+encodeURIComponent(q)+(cursor?'&cursor='+encodeURIComponent(cursor):'')),
 label:x=>x.name,onSelect:previewName,onClear:previewName
});
function render(){let q=search.value.trim().toLowerCase();list.textContent='';let rows=products.filter(x=>!q||[x.name,x.item,x.brand,x.barcode].some(v=>String(v||'').toLowerCase().includes(q)));if(!rows.length){let p=document.createElement('p');p.className='hint catalog-empty-hint';p.textContent=q?'Nenhum produto encontrado. Tente outro nome ou código.':'Nenhum produto cadastrado. Toque em “+ Novo produto” para começar.';list.append(p);return}rows.forEach(x=>{let b=document.createElement('button');b.type='button';b.className='catalog-parent-card'+(x.active?'':' is-inactive');let s=document.createElement('strong'),m=document.createElement('small');s.textContent=x.name||x.item;let meta=[x.brand,(x.barcode?'EAN '+x.barcode:''),'1 un = '+displayDecimal(x.content_quantity,4)+' '+x.unit].filter(Boolean);m.textContent=meta.join(' · ');b.append(s,m);if(!x.active){let badge=document.createElement('span');badge.className='badge';badge.textContent='Inativo';b.append(badge)}b.onclick=()=>edit(x,b);list.append(b)})}
async function load(){products=await api('/api/catalog/products');render()}
function clearSelection(){list.querySelectorAll('.catalog-parent-card.selected').forEach(x=>x.classList.remove('selected'))}
function close(){editing=null;parentSelect.clear();brandSelect.clear();form.reset();form.hidden=true;if(dialog.open)dialog.close();parentLookup.hidden=true;brandLookup.hidden=true;clearSelection();formMsg('','')}

// Criação com recuperação: o envio fica guardado até o servidor confirmar. Repetir o envio devolve o produto original, sem duplicar.
const describe=p=>'“'+(p.label||'Produto')+'”';
async function afterCreate(out,p){let handoff=p.handoff;if(handoff&&handoff.return_to==='/entry'){location.href='/entry?'+new URLSearchParams({sku_id:String(out.id),item:handoff.item,brand:handoff.brand,barcode:p.body.barcode,unit:handoff.unit,content_quantity:p.body.content_quantity}).toString();return}search.value='';await load();msg('success',p.label+(out&&out.replayed?' já estava cadastrado.':' criado.'))}
const flow=catalogPendingFlow({kind:'catalog-sku',box:$('productPending'),text:$('productPendingText'),retry:$('productRetry'),discard:$('productDiscard'),lock:[$('productNew')],feedback:status,describe,onConfirmed:afterCreate});

function fresh(){if(flow.pending())return;editing=null;clearSelection();form.reset();$('productId').value='';$('productActive').checked=true;content.value='1';parentSelect.clear();brandSelect.clear();parentSelect.setDisabled(false);brandSelect.setDisabled(false);$('productDialogTitle').textContent='Novo produto';setCrudMode(form,false,'Criar produto','Salvar produto');form.hidden=false;if(!dialog.open)dialog.showModal();msg('','');formMsg('','');previewName();parentSearch.focus();parentSelect.load('').catch(e=>formMsg('error',uiErrorMessage(e,'Não foi possível carregar os itens.')))}
function edit(x,card){editing=x;clearSelection();card?.classList.add('selected');$('productId').value=x.id;parentSelect.setSelected({id:x.item_id,name:x.item,unit:x.unit,active:true});brandSelect.setSelected({id:x.brand_id,name:x.brand,active:true});name.value=x.name||'';content.value=x.content_quantity;barcode.value=x.barcode;$('productActive').checked=!!x.active;parentSelect.setDisabled(!!x.has_history);brandSelect.setDisabled(!!x.has_history);$('productDialogTitle').textContent='Editar produto';setCrudMode(form,true,'Criar produto','Salvar produto');form.hidden=false;if(!dialog.open)dialog.showModal();msg('','');formMsg('','');previewName();name.focus()}
$('productNew').onclick=fresh;
$('productCancel').onclick=close;
dialog.addEventListener('cancel',()=>{form.hidden=true;parentLookup.hidden=true;brandLookup.hidden=true;clearSelection()});
search.oninput=render;name.oninput=previewName;content.oninput=previewName;
form.onsubmit=async e=>{e.preventDefault();let submit=e.submitter||form.querySelector('button[type="submit"]');if(submit?.dataset.busy==='1')return;let selectedParent=currentItem(),selectedBrand=currentBrand();if(!selectedParent){formMsg('error','Escolha um item.');parentSearch.focus();return}if(!selectedBrand){formMsg('error','Escolha uma marca.');brandSearch.focus();return}if(!requireFields(form,formStatus))return;await runLocked(submit,async()=>{let wasEditing=!!editing,body={item_id:+selectedParent.id,brand_id:+selectedBrand.id,content_quantity:requestDecimal(content.value),name:name.value.trim(),barcode:normalizeBarcode(barcode.value),active:$('productActive').checked};let label=body.name||'Produto';formMsg('loading',(editing?'Atualizando ':'Criando ')+label+'…');
if(!editing){body.operation_id=newOperationID();let params=new URLSearchParams(location.search),ret=params.get('return_to'),p={url:'/api/catalog/skus',body,label,handoff:ret==='/entry'?{return_to:ret,item:selectedParent.name,brand:selectedBrand.name,unit:selectedParent.unit}:null};let res=await flow.submit(p);if(res.state==='refused'){let m=String(res.err&&res.err.message||'').toLowerCase();formMsg('error',res.err.status===409&&m.includes('código de barras')?'Código de barras '+body.barcode+' já existe no banco de dados.':uiErrorMessage(res.err,'Não foi possível salvar '+label+'.'));return}if(!(res.state==='confirmed'&&p.handoff))close();return}
body.version=editing.version;try{await api('/api/catalog/skus/'+editing.id,{method:'PATCH',body:JSON.stringify(body)});close();search.value='';await load();msg('success',label+(wasEditing?' atualizado.':' criado.'))}catch(err){let m=String(err&&err.message||'').toLowerCase();if(err.status===409&&m.includes('código de barras')){formMsg('error','Código de barras '+body.barcode+' já existe no banco de dados.');return}formMsg('error',uiErrorMessage(err,'Não foi possível salvar '+label+'.'))}})};
initScanner('productScan',code=>{barcode.value=normalizeBarcode(code);name.focus()});
try{await Promise.all([load(),flow.refresh()]);let params=new URLSearchParams(location.search),incoming=normalizeBarcode(params.get('barcode')),incomingQuery=String(params.get('q')||'').trim();if(incoming&&!flow.pending()){fresh();barcode.value=incoming}else if(incomingQuery){search.value=incomingQuery;render();search.focus()}}catch(e){msg('error',uiErrorMessage(e,'Não foi possível carregar os produtos.'))}
})();