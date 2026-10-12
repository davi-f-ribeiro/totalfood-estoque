(async()=>{
const root=document.getElementById('recipesRoot');if(!root)return;
const canWrite=root.dataset.canWrite==='1',$=id=>document.getElementById(id);
const list=$('recipeList'),detail=$('recipeDetail'),search=$('recipeSearch'),status=$('recipeStatus'),customerList=$('customerList');
const dlg=$('recipeDialog'),form=$('recipeForm'),fStatus=$('recipeFormStatus'),linesBox=$('recipeLines'),pricesBox=$('recipePrices');
const cDlg=$('customerDialog'),cForm=$('customerForm'),cStatus=$('customerFormStatus');
const nf=(min,max)=>new Intl.NumberFormat('pt-BR',{minimumFractionDigits:min,maximumFractionDigits:max});
const money=(v,max=2)=>v==null?'—':'R$ '+nf(2,max).format(Number(v));
const percent=v=>v==null?'—':nf(1,1).format(Number(v))+'%';
const qty=v=>nf(0,4).format(Number(v));
const inputDec=v=>String(Number(v)).replace('.',',');
let recipes=[],customers=[],options=null,selectedId=0,selectedCustomer=0,current=null,seq=0;
function el(tag,cls,text){let e=document.createElement(tag);if(cls)e.className=cls;if(text!=null)e.textContent=text;return e}
function clearSel(){list.querySelectorAll('.catalog-parent-card.selected').forEach(x=>x.classList.remove('selected'))}
async function loadOptions(){if(options)return options;options=await api('/api/recipes/options');return options}
function renderList(){
 list.textContent='';let q=search.value.trim().toLowerCase();
 let rows=recipes.filter(r=>!q||r.name.toLowerCase().includes(q));
 if(!rows.length){list.append(el('p','hint',recipes.length?'Nenhuma ficha encontrada. Revise a busca.':'Nenhuma ficha cadastrada.'));return}
 rows.forEach(r=>{let b=el('button','catalog-parent-card'),s=el('strong',null,r.name),m=el('small',null,r.incomplete?'Sem custo em algum ingrediente':'Custo da porção: '+money(r.cost_per_portion,4));
  b.type='button';if(r.id===selectedId)b.classList.add('selected');if(!r.active)m.textContent+=' · Inativa';
  b.append(s,m);b.onclick=()=>{selectedId=r.id;selectedCustomer=0;renderList();showRecipe(r.id)};list.append(b)})}
function renderCustomers(){
 customerList.textContent='';
 if(!customers.length){customerList.append(el('p','hint','Nenhum cliente cadastrado.'));return}
 customers.forEach(c=>{let b=el('button','catalog-parent-card'),s=el('strong',null,c.name);b.type='button';b.append(s);
  if(!c.active)b.append(el('small',null,'Inativo'));
  if(canWrite)b.onclick=()=>openCustomer(c);else{b.classList.add('readonly');b.disabled=true}
  customerList.append(b)})}
async function loadAll(){
 setFeedback(status,'loading','Carregando fichas…');
 try{[recipes,customers]=await Promise.all([api('/api/recipes'),api('/api/customers')])}catch(err){setFeedback(status,'error',uiErrorMessage(err,'Não foi possível carregar as fichas. Atualize a página.'));return false}
 setFeedback(status,'','');renderList();renderCustomers();return true}
function kpi(label,value){let d=el('div','recipe-kpi');d.append(el('small',null,label),el('strong',null,value));return d}
function fillPlanningLists(){[['recipeFunctions','meal_function'],['recipeTagList','tags']].forEach(([id,k])=>{let set=new Set();recipes.forEach(r=>[].concat(r[k]||[]).forEach(v=>v&&set.add(v)));let dl=$(id);dl.textContent='';[...set].sort().forEach(v=>{let o=document.createElement('option');o.value=v;dl.append(o)})})}
function renderDetail(d){
 detail.textContent='';current=d;
 let head=el('div','recipe-head'),t=el('div');t.append(el('h2',null,d.name),el('p','hint',nf(0,3).format(Number(d.portions))+' porção(ões) por receita'+(d.meal_function?' · '+d.meal_function:'')+(d.portion_size?' · porção pronta de '+nf(0,3).format(Number(d.portion_size))+' '+d.portion_unit:'')+((d.tags||[]).length?' · '+d.tags.join(', '):'')+(d.active?'':' · Ficha inativa')));
 head.append(t);if(canWrite){let e=el('button','secondary','Editar ficha');e.type='button';e.onclick=()=>openRecipe(d);head.append(e)}
 detail.append(head);
 if(d.incomplete){let w=el('p','impact-warning','Falta custo em: '+d.lines.filter(l=>l.avg_cost==null).map(l=>l.item).join(', ')+'. Registre uma entrada com preço no estoque para calcular o CMV.');detail.append(w)}
 let price=(d.prices||[]).find(p=>p.customer_id===selectedCustomer)||(d.prices||[])[0];
 if(price)selectedCustomer=price.customer_id;
 if((d.prices||[]).length){
  let pick=el('label','recipe-customer-pick','Cliente'),sel=document.createElement('select');
  d.prices.forEach(p=>{let o=document.createElement('option');o.value=p.customer_id;o.textContent=p.customer;sel.append(o)});
  sel.value=String(selectedCustomer);sel.onchange=()=>{selectedCustomer=Number(sel.value);renderDetail(d)};pick.append(sel);detail.append(pick)}
 else detail.append(el('p','hint','Nenhum preço de venda cadastrado. Edite a ficha para adicionar um cliente.'));
 let k=el('div','recipe-kpis');
 k.append(kpi('CMV',percent(price&&price.cmv_percent)),kpi('Custo da porção',money(d.cost_per_portion,4)),kpi('Preço de venda',money(price&&price.sale_price)),kpi('Margem',percent(price&&price.margin_percent)));
 detail.append(k);
 let tbl=el('table','recipe-table'),th=el('thead'),hr=el('tr');
 ['Ingrediente','Quantidade','Custo unitário','Custo total'].forEach(h=>hr.append(el('th',null,h)));th.append(hr);
 let tb=el('tbody');
 d.lines.forEach(l=>{let tr=el('tr'),c1=el('td',null,l.item),q=qty(l.quantity)+' '+l.unit;
  if(Number(l.correction_factor)>1)q+=' (bruto '+qty(l.gross_quantity)+')';
  let c2=el('td',null,q),c3=el('td',l.avg_cost==null?'recipe-nocost':null,l.avg_cost==null?'Sem custo':money(l.avg_cost,4)),c4=el('td',l.line_cost==null?'recipe-nocost':null,l.line_cost==null?'Sem custo':money(l.line_cost));
  [[c1,'Ingrediente'],[c2,'Quantidade'],[c3,'Custo unitário'],[c4,'Custo total']].forEach(([c,n])=>{c.dataset.label=n;tr.append(c)});tb.append(tr)});
 let tf=el('tfoot'),fr=el('tr'),f1=el('td',null,'Custo da receita'),f2=el('td'),f3=el('td'),f4=el('td',null,money(d.total_cost));
 f1.dataset.label='';f4.dataset.label='Total';fr.append(f1,f2,f3,f4);f2.hidden=f3.hidden=true;tf.append(fr);
 tbl.append(th,tb,tf);detail.append(tbl)}
async function showRecipe(id){
 let my=++seq;detail.textContent='';detail.append(el('p','hint','Carregando ficha…'));
 try{let d=await api('/api/recipes/'+id);if(my!==seq)return;renderDetail(d)}catch(err){if(my!==seq)return;detail.textContent='';detail.append(el('p','impact-warning',uiErrorMessage(err,'Não foi possível carregar a ficha. Tente de novo.')))}}
function optionSelect(rows,value,ph){let s=document.createElement('select');s.required=true;let p=document.createElement('option');p.value='';p.textContent=ph;s.append(p);
 rows.forEach(r=>{let o=document.createElement('option');o.value=r.id;o.textContent=r.name+(r.unit?' ('+r.unit+')':'');s.append(o)});if(value)s.value=String(value);return s}
function labelled(text,ctrl){let l=document.createElement('label');l.append(text,ctrl);return l}
function removeBtn(row){let b=el('button','secondary','Remover');b.type='button';b.onclick=()=>row.remove();return b}
function addLine(l){
 let row=el('div','recipe-row'),sel=optionSelect(options.items,l&&l.item_id,'Escolha o ingrediente'),q=document.createElement('input'),f=document.createElement('input');
 q.inputMode=f.inputMode='decimal';q.required=f.required=true;q.autocomplete=f.autocomplete='off';q.value=l?inputDec(l.quantity):'';f.value=l?inputDec(l.correction_factor):'1';
 sel.dataset.role='item';q.dataset.role='qty';f.dataset.role='factor';
 row.append(labelled('Ingrediente',sel),labelled('Quantidade',q),labelled('Fator de correção',f),removeBtn(row));linesBox.append(row)}
function addPrice(p){
 let row=el('div','recipe-row recipe-price-row'),sel=optionSelect(options.customers,p&&p.customer_id,'Escolha o cliente'),v=document.createElement('input');
 v.inputMode='decimal';v.required=true;v.autocomplete='off';v.value=p?inputDec(p.sale_price):'';sel.dataset.role='customer';v.dataset.role='price';
 row.append(labelled('Cliente',sel),labelled('Preço de venda (R$)',v),removeBtn(row));pricesBox.append(row)}
async function openRecipe(d){
 try{await loadOptions()}catch(err){setFeedback(status,'error',uiErrorMessage(err,'Não foi possível carregar os itens e clientes. Tente de novo.'));return}
 form.reset();linesBox.textContent='';pricesBox.textContent='';setFeedback(fStatus,'','');
 $('recipeId').value=d?d.id:'';$('recipeName').value=d?d.name:'';$('recipePortions').value=d?inputDec(d.portions):'1';$('recipeNotes').value=d?d.notes||'':'';$('recipeActive').checked=d?d.active:true;
 $('recipeFunction').value=d?d.meal_function||'':'';$('recipePortionSize').value=d&&d.portion_size?inputDec(d.portion_size):'';$('recipePortionUnit').value=d?d.portion_unit||'':'';$('recipeTags').value=d?(d.tags||[]).join(', '):'';fillPlanningLists();
 form.dataset.version=d?d.version:'';(d?d.lines:[null]).forEach(addLine);(d?d.prices:[]).forEach(addPrice);
 setCrudMode(form,!!d,'Criar ficha','Atualizar ficha');$('recipeDialogTitle').textContent=d?'Editar ficha':'Nova ficha';
 form.hidden=false;dlg.showModal();$('recipeName').focus()}
function closeRecipe(){form.hidden=true;if(dlg.open)dlg.close()}
$('recipeCancel').onclick=closeRecipe;
$('recipeAddLine').onclick=()=>addLine(null);$('recipeAddPrice').onclick=()=>addPrice(null);
form.addEventListener('submit',async e=>{e.preventDefault();
 let submit=form.querySelector('button[type="submit"]');
 await runLocked(submit,async()=>{
  if(!requireFields(form,fStatus))return;
  let lines=[...linesBox.children].map(r=>({item_id:Number(r.querySelector('[data-role=item]').value),quantity:requestDecimal(r.querySelector('[data-role=qty]').value),correction_factor:requestDecimal(r.querySelector('[data-role=factor]').value)}));
  if(!lines.length){setFeedback(fStatus,'error','Adicione ao menos um ingrediente.');return}
  let prices=[...pricesBox.children].map(r=>({customer_id:Number(r.querySelector('[data-role=customer]').value),sale_price:requestDecimal(r.querySelector('[data-role=price]').value)}));
  let id=$('recipeId').value,name=$('recipeName').value.trim(),body={name,portions:requestDecimal($('recipePortions').value),notes:$('recipeNotes').value.trim(),active:$('recipeActive').checked,lines,prices,meal_function:$('recipeFunction').value.trim(),portion_size:requestDecimal($('recipePortionSize').value),portion_unit:$('recipePortionUnit').value,tags:$('recipeTags').value.split(',').map(x=>x.trim()).filter(Boolean)};
  if(id)body.version=Number(form.dataset.version);
  setFeedback(fStatus,'loading','Salvando ficha…');
  try{let r=await api(id?'/api/recipes/'+id:'/api/recipes',{method:id?'PATCH':'POST',body:JSON.stringify(body)});
   closeRecipe();selectedId=Number(id||r.id);await loadAll();await showRecipe(selectedId);setFeedback(status,'success','Ficha '+name+(id?' atualizada.':' criada.'))}
  catch(err){setFeedback(fStatus,'error',uiErrorMessage(err,'Não foi possível salvar a ficha. Revise os dados e tente de novo.'))}})});
function openCustomer(c){
 cForm.reset();setFeedback(cStatus,'','');$('customerId').value=c?c.id:'';$('customerName').value=c?c.name:'';$('customerActive').checked=c?c.active:true;cForm.dataset.version=c?c.version:'';
 setCrudMode(cForm,!!c,'Criar cliente','Atualizar cliente');$('customerDialogTitle').textContent=c?'Editar cliente':'Novo cliente';cForm.hidden=false;cDlg.showModal();$('customerName').focus()}
function closeCustomer(){cForm.hidden=true;if(cDlg.open)cDlg.close()}
$('customerCancel').onclick=closeCustomer;
cForm.addEventListener('submit',async e=>{e.preventDefault();
 await runLocked(cForm.querySelector('button[type="submit"]'),async()=>{
  if(!requireFields(cForm,cStatus))return;
  let id=$('customerId').value,name=$('customerName').value.trim(),body={name,active:$('customerActive').checked};if(id)body.version=Number(cForm.dataset.version);
  setFeedback(cStatus,'loading','Salvando cliente…');
  try{await api(id?'/api/customers/'+id:'/api/customers',{method:id?'PATCH':'POST',body:JSON.stringify(body)});
   options=null;closeCustomer();await loadAll();if(selectedId)await showRecipe(selectedId);setFeedback(status,'success','Cliente '+name+(id?' atualizado.':' criado.'))}
  catch(err){setFeedback(cStatus,'error',uiErrorMessage(err,'Não foi possível salvar o cliente. Revise o nome e tente de novo.'))}})});
if(canWrite){$('recipeNew').onclick=()=>openRecipe(null);$('customerNew').onclick=()=>openCustomer(null)}
search.addEventListener('input',renderList);
await loadAll();
})();
