(async()=>{
const root=document.getElementById('plannerRoot');if(!root)return;
const canWrite=root.dataset.canWrite==='1',$=id=>document.getElementById(id);
const MEALS=[['cafe','Café'],['almoco','Almoço'],['lanche','Lanche'],['jantar','Jantar']];
const RULES=[['exists_without','Exigir opção sem a etiqueta'],['exists_with','Exigir opção com a etiqueta'],['none_with','Proibir a etiqueta'],['no_repeat','Não repetir em dias seguidos']];
const mealName=m=>(MEALS.find(x=>x[0]===m)||[m,m])[1];
const nf=(min,max)=>new Intl.NumberFormat('pt-BR',{minimumFractionDigits:min,maximumFractionDigits:max});
const money=v=>v==null?'—':(Number(v)<0?'-':'')+'R$ '+nf(2,2).format(Math.abs(Number(v)));
const pct=v=>v==null?'—':nf(1,2).format(Number(v))+'%';
const inputDec=v=>v==null||v===''?'':String(Number(v)).replace('.',',');
const cap=s=>s?s.charAt(0).toUpperCase()+s.slice(1):s;
function amount(v,unit){let n=Number(v);if(unit==='g'&&n>=1000)return nf(0,1).format(n/1000)+' kg';if(unit==='ml'&&n>=1000)return nf(0,1).format(n/1000)+' L';return nf(0,unit==='un'?0:1).format(n)+' '+(unit||'')}
function dayLabel(d){let [y,m,dd]=d.split('-').map(Number),w=new Date(y,m-1,dd).toLocaleDateString('pt-BR',{weekday:'short'}).replace('.','');return w+' '+String(dd).padStart(2,'0')+'/'+String(m).padStart(2,'0')}
function el(tag,cls,text){let e=document.createElement(tag);if(cls)e.className=cls;if(text!=null)e.textContent=text;return e}
function labelled(text,ctrl,cls){let l=el('label',cls);l.append(text,ctrl);return l}
function input(value,attrs){let i=document.createElement('input');i.autocomplete='off';Object.assign(i,attrs||{});if(value!=null)i.value=value;return i}
function select(rows,value,placeholder){let s=document.createElement('select');if(placeholder!=null){let p=document.createElement('option');p.value='';p.textContent=placeholder;s.append(p)}rows.forEach(([v,t])=>{let o=document.createElement('option');o.value=v;o.textContent=t;s.append(o)});if(value!=null)s.value=String(value);return s}
function button(text,cls,fn){let b=el('button',cls,text);b.type='button';b.onclick=fn;return b}
function kpi(label,value){let d=el('div','recipe-kpi');d.append(el('small',null,label),el('strong',null,value));return d}
function cell(tr,text,label,cls){let td=el('td',cls,text);td.dataset.label=label;tr.append(td);return td}
function table(headers){let t=el('table','recipe-table'),h=el('thead'),r=el('tr');headers.forEach(x=>r.append(el('th',null,x)));h.append(r);t.append(h,el('tbody'));return t}
function warn(title,lines){let box=el('div','impact-warning');box.append(el('strong',null,title));let ul=el('ul');lines.forEach(x=>ul.append(el('li',null,x)));box.append(ul);return box}
const status=$('plannerStatus'),planList=$('planList'),groupList=$('groupList'),detail=$('planDetail');
let options={groups:[],recipes:[],functions:[],tags:[]},plans=[],current=null,selectedPlan=0,seq=0,edits=new Map(),premise=new Map(),lastSolve=null;
const posKey=p=>[p.group_id,p.day,p.meal,p.meal_function,p.option].join('|');
const mealKey=m=>[m.group_id,m.day,m.meal].join('|');

function fillDatalists(){[['plannerFunctions',options.functions],['plannerTags',options.tags]].forEach(([id,rows])=>{let dl=$(id);dl.textContent='';rows.forEach(v=>{let o=document.createElement('option');o.value=v;dl.append(o)})})}
function renderPlans(){
 planList.textContent='';
 if(!plans.length){planList.append(el('p','hint','Nenhum plano criado.'));return}
 plans.forEach(p=>{let b=el('button','catalog-parent-card');b.type='button';if(p.id===selectedPlan)b.classList.add('selected');
  b.append(el('strong',null,p.name),el('small',null,dayLabel(p.start_date)+' a '+dayLabel(p.end_date)+' · '+(p.status==='approved'?'Aprovado':'Rascunho')));
  b.onclick=()=>{selectedPlan=p.id;renderPlans();showPlan(p.id)};planList.append(b)})}
function renderGroups(){
 groupList.textContent='';
 if(!options.groups.length){groupList.append(el('p','hint','Nenhum grupo cadastrado. Crie o grupo com as refeições e o per capita do contrato.'));return}
 options.groups.forEach(g=>{let b=el('button','catalog-parent-card');b.type='button';
  b.append(el('strong',null,g.name),el('small',null,g.meals.map(m=>mealName(m.meal)+' '+money(m.price)).join(' · ')+(g.active?'':' · Inativo')));
  if(canWrite)b.onclick=()=>openGroup(g);else{b.classList.add('readonly');b.disabled=true}
  groupList.append(b)})}
async function loadAll(){
 setFeedback(status,'loading','Carregando planos…');
 try{[plans,options]=await Promise.all([api('/api/plans'),api('/api/planner/options')])}catch(err){setFeedback(status,'error',uiErrorMessage(err,'Não foi possível carregar o planejamento. Atualize a página.'));return false}
 setFeedback(status,'','');fillDatalists();renderPlans();renderGroups();return true}

// ---------- Plano ----------
async function showPlan(id){
 let my=++seq;detail.textContent='';detail.append(el('p','hint','Carregando plano…'));
 try{let d=await api('/api/plans/'+id);if(my!==seq)return;edits=new Map();premise=new Map();renderPlan(d)}
 catch(err){if(my!==seq)return;detail.textContent='';detail.append(el('p','impact-warning',uiErrorMessage(err,'Não foi possível carregar o plano. Tente de novo.')))}}
function recipeChoices(fn,currentId){return options.recipes.filter(r=>r.meal_function===fn&&(r.active||r.id===currentId)).map(r=>[r.id,r.name])}
function dirty(){return edits.size>0||premise.size>0}
function syncSave(){let b=$('planSave');if(b)b.disabled=!dirty();let s=$('planSolve'),ap=$('planApprove');if(s)s.disabled=dirty();if(ap)ap.disabled=dirty()}
function renderPlan(d){
 current=d;detail.textContent='';
 const p=d.plan,draft=p.status==='draft',edit=draft&&canWrite;
 let head=el('div','recipe-head'),t=el('div');
 t.append(el('h2',null,p.name),el('p','hint',dayLabel(p.start_date)+' a '+dayLabel(p.end_date)+' · CMV alvo '+pct(p.target_cmv)+' · desvio diário até '+pct(p.daily_tolerance)+' · '+(draft?'Rascunho':'Aprovado')));
 head.append(t);
 let actions=el('div','actions planner-actions');
 if(edit){
  let save=button('Salvar alterações','',saveEdits);save.id='planSave';
  actions.append(save,Object.assign(button('Sugerir com o solver','secondary',solve),{id:'planSolve'}),Object.assign(button('Aprovar plano','secondary',approve),{id:'planApprove'}));
 }else if(canWrite&&!draft){actions.append(button('Emitir solicitação de compra','',emitPurchase))}
 head.append(actions);detail.append(head);
 if(d.show_costs&&d.summary){let k=el('div','recipe-kpis');k.append(kpi('Faturamento previsto',money(d.summary.revenue)),kpi('Gasto previsto',money(d.summary.cost)),kpi('CMV previsto',pct(d.summary.cmv)),kpi('Margem prevista',money(d.summary.margin)));detail.append(k)}
 if(d.blocked.length)detail.append(warn('O cálculo ainda não pode ser feito',d.blocked));
 if(d.violations.length)detail.append(warn('Regras quebradas',d.violations.map(v=>groupName(v.group_id)+', '+dayLabel(v.day)+', '+mealName(v.meal)+': '+v.message)));
 d.groups.forEach(g=>detail.append(groupGrid(d,g,edit,!draft)));
 if(d.lots&&d.lots.length)detail.append(lotsSection(d));
 if(d.items&&d.items.length)detail.append(itemsSection(d));
 if(!draft){let pr=el('section','planner-section');pr.id='purchaseSection';detail.append(pr);loadPurchases();let cmp=el('section','planner-section');cmp.id='comparisonSection';detail.append(cmp);loadComparison()}
 syncSave()}
function groupName(id){let g=(current&&current.groups||[]).find(x=>x.id===id)||options.groups.find(x=>x.id===id);return g?g.name:'Grupo'}
function groupGrid(d,g,edit,approved){
 let sec=el('section','planner-section'),gr=(d.group_results||[]).find(x=>x.group_id===g.id);
 let h=el('div','section-heading');h.append(el('h3',null,g.name));if(gr)h.append(el('p','hint','Faturamento '+money(gr.revenue)+' · gasto '+money(gr.cost)+' · CMV '+pct(gr.cmv)));sec.append(h);
 let meals=d.meals.filter(m=>m.group_id===g.id),mealIds=MEALS.map(x=>x[0]).filter(m=>meals.some(x=>x.meal===m));
 let wrap=el('div','planner-grid-wrap'),tbl=table(['Prato'].concat(d.days.map(dayLabel)));tbl.classList.add('planner-grid');let tb=tbl.tBodies[0];
 mealIds.forEach(meal=>{
  let mh=el('tr','planner-meal-row'),th=el('th',null,mealName(meal));th.colSpan=d.days.length+1;mh.append(th);tb.append(mh);
  let byDay=Object.fromEntries(meals.filter(m=>m.meal===meal).map(m=>[m.day,m]));
  let pr=el('tr');cell(pr,'Refeições previstas','');d.days.forEach(day=>{let m=byDay[day],td=cell(pr,m?null:'—',dayLabel(day));if(!m)return;
   if(edit){let i=input(m.people,{type:'number',min:0,step:1,inputMode:'numeric'});i.setAttribute('aria-label','Refeições previstas '+dayLabel(day));i.oninput=()=>{premise.set(mealKey(m),Number(i.value));syncSave()};td.append(i)}else td.textContent=nf(0,0).format(m.people)});tb.append(pr);
  if(approved){let ar=el('tr');cell(ar,'Quantidade real','');d.days.forEach(day=>{let m=byDay[day],td=cell(ar,m?null:'—',dayLabel(day));if(!m)return;
   if(canWrite){let i=input(m.actual_people==null?'':m.actual_people,{type:'number',min:0,step:1,inputMode:'numeric',placeholder:'Sem lançamento'});i.setAttribute('aria-label','Quantidade real '+dayLabel(day));i.onchange=()=>saveActual(m,i);td.append(i)}else td.textContent=m.actual_people==null?'Sem lançamento':nf(0,0).format(m.actual_people)});tb.append(ar)}
  let rows=[...new Set(d.positions.filter(x=>x.group_id===g.id&&x.meal===meal).map(x=>x.meal_function+'|'+x.option))];
  rows.forEach(rk=>{let [fn,opt]=rk.split('|'),tr=el('tr'),multi=d.positions.some(x=>x.group_id===g.id&&x.meal===meal&&x.meal_function===fn&&x.option>1);
   cell(tr,cap(fn)+(multi?' '+opt:''),'');
   d.days.forEach(day=>{let pos=d.positions.find(x=>x.group_id===g.id&&x.meal===meal&&x.day===day&&x.meal_function===fn&&String(x.option)===opt),td=cell(tr,pos?null:'—',dayLabel(day),'planner-cell');if(!pos)return;
    let box=el('div','planner-cellbox');td.append(box);
    if(edit)box.append(positionEditor(pos));else{box.append(el('span',null,pos.recipe||'Sem ficha'));if(pos.locked)box.append(el('small','hint','Travada'))}
    if(pos.produced&&pos.recipe_id)box.append(el('small','hint',amount(pos.produced,pos.unit)));
   });tb.append(tr)});
  if(d.meal_results){let cr=el('tr','planner-cmv-row');cell(cr,'CMV da refeição','');d.days.forEach(day=>{let r=d.meal_results.find(x=>x.group_id===g.id&&x.day===day&&x.meal===meal);cell(cr,r?pct(r.cmv):'—',dayLabel(day))});tb.append(cr)}
 });
 if(gr&&mealIds.length>1){let dr=el('tr','planner-cmv-row');cell(dr,'CMV do dia','');d.days.forEach(day=>{let x=gr.days.find(y=>y.day===day);let td=cell(dr,x?pct(x.cmv):'—',dayLabel(day));if(x&&x.cmv!=null&&Number(x.cmv)>Number(d.plan.target_cmv)+Number(d.plan.daily_tolerance))td.classList.add('recipe-nocost')});tb.append(dr)}
 wrap.append(tbl);sec.append(wrap);return sec}
function positionEditor(pos){
 let box=el('div','planner-pick'),key=posKey(pos),sel=select(recipeChoices(pos.meal_function,pos.recipe_id),pos.recipe_id||'','Escolha a ficha');
 sel.setAttribute('aria-label',cap(pos.meal_function)+' '+dayLabel(pos.day));
 let lock=input(null,{type:'checkbox'});lock.checked=pos.locked;lock.disabled=!pos.recipe_id;
 const record=()=>{let rid=Number(sel.value||0);if(!rid)lock.checked=false;lock.disabled=!rid;
  if(rid===pos.recipe_id&&lock.checked===pos.locked)edits.delete(key);else edits.set(key,Object.assign({},pos,{recipe_id:rid,locked:lock.checked}));syncSave()};
 sel.onchange=record;lock.onchange=record;
 box.append(sel,labelled(' Travar',lock,'check planner-lock'));return box}
function payload(extra){let p=current.plan;return Object.assign({name:p.name,start_date:p.start_date,end_date:p.end_date,target_cmv:p.target_cmv,daily_tolerance:p.daily_tolerance,scale_tolerance:p.scale_tolerance,version:p.version},extra)}
function positionsPayload(list){return list.map(x=>({group_id:x.group_id,day:x.day,meal:x.meal,meal_function:x.meal_function,option:x.option,recipe_id:x.recipe_id||0,locked:!!x.locked,adhesion:x.adhesion}))}
async function saveEdits(){
 let b=$('planSave');await runLocked(b,async()=>{
  let extra={positions:positionsPayload([...edits.values()])};
  if(premise.size)extra.meals=current.meals.map(m=>({group_id:m.group_id,day:m.day,meal:m.meal,people:premise.has(mealKey(m))?premise.get(mealKey(m)):m.people}));
  if(extra.meals&&extra.meals.some(m=>!Number.isInteger(m.people)||m.people<0)){setFeedback(status,'error','Digite a quantidade prevista como número inteiro, zero ou mais.');return}
  setFeedback(status,'loading','Salvando o plano…');
  try{await api('/api/plans/'+current.plan.id,{method:'PATCH',body:JSON.stringify(payload(extra))});await showPlan(current.plan.id);setFeedback(status,'success','Plano salvo.')}
  catch(err){setFeedback(status,'error',uiErrorMessage(err,'Não foi possível salvar o plano. Tente de novo.'))}})}
async function solve(){
 let b=$('planSolve');await runLocked(b,async()=>{
  setFeedback(status,'loading','Calculando a sugestão…');
  try{lastSolve=await api('/api/plans/'+current.plan.id+'/solve',{method:'POST',body:'{}'});setFeedback(status,'','');showSolve(lastSolve)}
  catch(err){setFeedback(status,'error',uiErrorMessage(err,'Não foi possível calcular a sugestão. Tente de novo.'))}})}
function showSolve(s){
 let body=$('solveBody');body.textContent='';setFeedback($('solveStatus'),'','');
 if(current.show_costs){let k=el('div','recipe-kpis');k.append(kpi('CMV antes',pct(s.before.cmv)),kpi('CMV depois',pct(s.after.cmv)),kpi('Gasto depois',money(s.after.cost)));body.append(k)}
 if(!s.changes.length)body.append(el('p','hint','Nenhuma troca melhora o plano. As travas e regras já estão atendidas, ou não há ficha melhor.'));
 else{let t=table(['Onde','De','Para','Variação de custo']);t.classList.add('planner-text');s.changes.forEach(c=>{let tr=el('tr'),delta=-Number(c.saving);cell(tr,c.group+', '+dayLabel(c.day)+', '+mealName(c.meal)+', '+c.meal_function,'Onde');cell(tr,c.from||'Sem ficha','De');cell(tr,c.to,'Para');cell(tr,current.show_costs?(delta>0?'+':'')+money(delta):'—','Variação de custo');t.tBodies[0].append(tr)});body.append(t)}
 if(s.unmet.length)body.append(warn('O que a sugestão não resolve',s.unmet));
 if(s.ignored.length)body.append(el('p','hint','Fichas ignoradas por falta de custo: '+s.ignored.join(', ')+'.'));
 $('solveAccept').disabled=!s.changes.length;$('solveDialog').showModal()}
$('solveCancel').onclick=()=>$('solveDialog').close();
$('solveAccept').onclick=async()=>{let b=$('solveAccept');await runLocked(b,async()=>{
 let changed=new Set(lastSolve.changes.map(c=>[c.group_id,c.day,c.meal,c.meal_function,c.option].join('|')));
 let list=lastSolve.positions.filter(p=>changed.has(posKey(p)));
 setFeedback($('solveStatus'),'loading','Gravando a sugestão…');
 try{await api('/api/plans/'+current.plan.id,{method:'PATCH',body:JSON.stringify(payload({version:lastSolve.version,positions:positionsPayload(list)}))});$('solveDialog').close();await showPlan(current.plan.id);setFeedback(status,'success','Sugestão aplicada: '+list.length+' troca(s).')}
 catch(err){setFeedback($('solveStatus'),'error',uiErrorMessage(err,'Não foi possível gravar a sugestão. Atualize o plano e tente de novo.'))}})};
async function approve(){
 let b=$('planApprove');await runLocked(b,async()=>{
  if(b.dataset.confirm!=='1'){b.dataset.confirm='1';b.textContent='Confirmar aprovação';setFeedback(status,'','Depois de aprovado, o plano não muda: a premissa, as quantidades e os custos ficam gravados como previsto. Clique em “Confirmar aprovação” para seguir.');return}
  setFeedback(status,'loading','Aprovando o plano…');
  try{await api('/api/plans/'+current.plan.id+'/approve',{method:'POST',body:JSON.stringify({version:current.plan.version})});plans=await api('/api/plans');renderPlans();await showPlan(current.plan.id);setFeedback(status,'success','Plano aprovado.')}
  catch(err){setFeedback(status,'error',uiErrorMessage(err,'Não foi possível aprovar o plano. Tente de novo.'))}})}
async function saveActual(m,i){
 let v=i.value.trim();if(v===''){return}
 let n=Number(v);if(!Number.isInteger(n)||n<0){setFeedback(status,'error','Digite a quantidade real como número inteiro, zero ou mais.');return}
 i.disabled=true;setFeedback(status,'loading','Salvando a quantidade real…');
 try{let r=await api('/api/plans/'+current.plan.id+'/actuals',{method:'PATCH',body:JSON.stringify({group_id:m.group_id,day:m.day,meal:m.meal,people:n,version:m.actual_version})});m.actual_version=r.version;m.actual_people=n;setFeedback(status,'success','Quantidade real de '+dayLabel(m.day)+' salva.');loadComparison()}
 catch(err){setFeedback(status,'error',uiErrorMessage(err,'Não foi possível salvar a quantidade real. Tente de novo.'))}
 finally{i.disabled=false}}
function lotsSection(d){
 let sec=el('section','planner-section');sec.append(el('h3',null,'Produção por dia'),el('p','hint','Mesma preparação para vários grupos no mesmo dia vira um lote só. A divisão mostra quanto vai para cada grupo.'));
 let t=table(['Dia','Preparação','Total pronto','Por grupo']);t.classList.add('planner-text');d.lots.forEach(l=>{let tr=el('tr');cell(tr,dayLabel(l.day),'Dia');cell(tr,l.recipe,'Preparação');cell(tr,amount(l.produced,l.unit),'Total pronto');cell(tr,l.by_group.map(g=>g.group+': '+amount(g.produced,l.unit)).join(' · '),'Por grupo');t.tBodies[0].append(tr)});sec.append(t);return sec}
function itemsSection(d){
 let sec=el('section','planner-section');sec.append(el('h3',null,'Ingredientes do período'));
 let heads=['Item','Necessidade','Saldo atual'];if(d.show_costs)heads.push('Custo unitário','Origem do custo','Custo');
 let t=table(heads);d.items.forEach(it=>{let tr=el('tr'),short=Number(it.quantity)>Number(it.balance);cell(tr,it.item,'Item');cell(tr,nf(0,3).format(Number(it.quantity))+' '+it.unit,'Necessidade');cell(tr,nf(0,3).format(Number(it.balance))+' '+it.unit,'Saldo atual',short?'recipe-nocost':null);
  if(d.show_costs){cell(tr,money(it.unit_cost),'Custo unitário');cell(tr,it.cost_source==='estoque'?'Preço médio':'Última compra','Origem do custo');cell(tr,money(it.cost),'Custo')}t.tBodies[0].append(tr)});
 sec.append(t);return sec}
async function loadPurchases(){
 let sec=$('purchaseSection');if(!sec)return;sec.textContent='';sec.append(el('h3',null,'Solicitações de compra'));
 try{let rows=await api('/api/plans/'+current.plan.id+'/purchase-requests');
  if(!rows.length){sec.append(el('p','hint',canWrite?'Nenhuma solicitação emitida. Use “Emitir solicitação de compra” para pedir o que falta.':'Nenhuma solicitação emitida.'));return}
  rows.forEach(r=>{sec.append(el('p','hint','Emitida em '+new Date(r.created_at).toLocaleString('pt-BR')));
   if(!r.lines.length){sec.append(el('p','hint','O estoque atual cobre o plano. Nada a comprar.'));return}
   let t=table(['Item','Necessidade','Saldo','Comprar','Acaba em']);r.lines.forEach(l=>{let tr=el('tr');cell(tr,l.item,'Item');cell(tr,nf(0,3).format(Number(l.need))+' '+l.unit,'Necessidade');cell(tr,nf(0,3).format(Number(l.balance))+' '+l.unit,'Saldo');cell(tr,nf(0,3).format(Number(l.quantity))+' '+l.unit,'Comprar');cell(tr,dayLabel(l.runout_date),'Acaba em');t.tBodies[0].append(tr)});sec.append(t)})}
 catch(err){sec.append(el('p','impact-warning',uiErrorMessage(err,'Não foi possível carregar as solicitações de compra.')))}}
async function emitPurchase(e){
 let b=e.currentTarget;await runLocked(b,async()=>{
  setFeedback(status,'loading','Emitindo a solicitação de compra…');
  try{let op=b.dataset.op||(b.dataset.op='compra-'+current.plan.id+'-'+Date.now()+'-'+Math.random().toString(36).slice(2));
   let r=await api('/api/plans/'+current.plan.id+'/purchase-requests',{method:'POST',body:JSON.stringify({operation_id:op})});delete b.dataset.op;
   setFeedback(status,'success',r.lines.length?'Solicitação emitida com '+r.lines.length+' item(ns).':'O estoque atual cobre o plano. Solicitação registrada sem itens.');loadPurchases()}
  catch(err){setFeedback(status,'error',uiErrorMessage(err,'Não foi possível emitir a solicitação. Tente de novo; o envio não será duplicado.'))}})}
async function loadComparison(){
 let sec=$('comparisonSection');if(!sec)return;sec.textContent='';sec.append(el('h3',null,'Previsto x realizado'),el('p','hint','Realizado = saídas do estoque no período, de todos os depósitos. Ajustes de inventário e transferências não entram.'));
 try{let c=await api('/api/plans/'+current.plan.id+'/comparison');
  if(c.summary){let k=el('div','recipe-kpis');k.append(kpi('Gasto previsto',money(c.summary.planned_cost)),kpi('Gasto realizado',money(c.summary.actual_cost)),kpi('CMV previsto',pct(c.summary.planned_cmv)),kpi('CMV realizado',c.summary.actual_cmv==null?'Falta lançar':pct(c.summary.actual_cmv)));sec.append(k)}
  if(c.meals_without_actual>0)sec.append(el('p','impact-warning',c.meals_without_actual+' refeição(ões) sem quantidade real. O faturamento e o CMV realizados aparecem quando todas forem lançadas.'));
  let heads=['Item','Previsto','Realizado','Diferença'];if(c.show_costs)heads.push('Custo previsto','Custo realizado','Diferença de custo');
  let t=table(heads);c.items.forEach(it=>{let tr=el('tr');cell(tr,it.item,'Item');cell(tr,nf(0,3).format(Number(it.planned_quantity))+' '+it.unit,'Previsto');cell(tr,nf(0,3).format(Number(it.actual_quantity))+' '+it.unit,'Realizado');cell(tr,nf(0,3).format(Number(it.diff_quantity))+' '+it.unit,'Diferença',Number(it.diff_quantity)>0?'recipe-nocost':null);
   if(c.show_costs){cell(tr,money(it.planned_cost),'Custo previsto');cell(tr,money(it.actual_cost),'Custo realizado');cell(tr,money(it.diff_cost),'Diferença de custo',Number(it.diff_cost)>0?'recipe-nocost':null)}t.tBodies[0].append(tr)});
  sec.append(t)}
 catch(err){sec.append(el('p','impact-warning',uiErrorMessage(err,'Não foi possível carregar o previsto x realizado.')))}}

// ---------- Novo plano ----------
const planDialog=$('planDialog'),planForm=$('planForm'),planFormStatus=$('planFormStatus');
function openPlan(){
 planForm.reset();setFeedback(planFormStatus,'','');let box=$('planPremise');box.textContent='';
 let active=options.groups.filter(g=>g.active);
 if(!active.length)box.append(el('p','hint','Cadastre um grupo de clientes antes de criar o plano.'));
 active.forEach(g=>{let row=el('div','planner-premise');row.append(el('strong',null,g.name));
  g.meals.forEach(m=>{let i=input('',{type:'number',min:0,step:1,inputMode:'numeric',placeholder:'0'});i.dataset.group=g.id;i.dataset.meal=m.meal;row.append(labelled(mealName(m.meal),i))});box.append(row)});
 planDialog.showModal();$('planName').focus()}
$('planCancel').onclick=()=>{planForm.hidden=true;planDialog.close()};
planForm.addEventListener('submit',async e=>{e.preventDefault();await runLocked(planForm.querySelector('button[type="submit"]'),async()=>{
 if(!requireFields(planForm,planFormStatus))return;
 let start=$('planStart').value,end=$('planEnd').value;
 if(end<start){setFeedback(planFormStatus,'error','A data final precisa ser igual ou depois da inicial.');return}
 let days=[];for(let d=new Date(start+'T12:00:00');d<=new Date(end+'T12:00:00');d.setDate(d.getDate()+1))days.push(d.toISOString().slice(0,10));
 let meals=[];$('planPremise').querySelectorAll('input[data-group]').forEach(i=>{let n=Number(i.value||0);if(n>0)days.forEach(day=>meals.push({group_id:Number(i.dataset.group),day,meal:i.dataset.meal,people:n}))});
 if(!meals.length){setFeedback(planFormStatus,'error','Digite a quantidade prevista de ao menos uma refeição.');return}
 setFeedback(planFormStatus,'loading','Criando o plano…');
 try{let r=await api('/api/plans',{method:'POST',body:JSON.stringify({name:$('planName').value.trim(),start_date:start,end_date:end,target_cmv:requestDecimal($('planTarget').value),daily_tolerance:requestDecimal($('planDaily').value),scale_tolerance:requestDecimal($('planScale').value),meals})});
  planForm.hidden=true;planDialog.close();selectedPlan=r.id;plans=await api('/api/plans');renderPlans();await showPlan(r.id);setFeedback(status,'success','Plano criado. Escolha os pratos ou peça uma sugestão ao solver.')}
 catch(err){setFeedback(planFormStatus,'error',uiErrorMessage(err,'Não foi possível criar o plano. Revise os dados e tente de novo.'))}})});

// ---------- Grupo de clientes ----------
const groupDialog=$('groupDialog'),groupForm=$('groupForm'),groupFormStatus=$('groupFormStatus');
function menuRow(f){
 let row=el('div','planner-menu-row'),fn=input(f?f.meal_function:'',{required:true});fn.setAttribute('list','plannerFunctions');fn.dataset.role='fn';
 let opt=input(f?f.options:1,{type:'number',min:1,max:9,step:1,required:true});opt.dataset.role='options';
 let pc=input(f?inputDec(f.per_capita):'',{inputMode:'decimal',required:true});pc.dataset.role='pc';
 let un=select([['g','g'],['ml','ml'],['un','un']],f?f.per_capita_unit:'g');un.dataset.role='unit';
 let ad=input(f?inputDec(f.adhesion):'100',{inputMode:'decimal',required:true});ad.dataset.role='adh';
 row.append(labelled('Função',fn),labelled('Opções',opt),labelled('Per capita pronto',pc),labelled('Unidade',un),labelled('Adesão (%)',ad),button('Remover','secondary',()=>row.remove()));return row}
function mealBlock(m){
 let box=el('div','planner-meal-block'),top=el('div','planner-meal-top');
 let meal=select(MEALS,m?m.meal:'almoco');meal.dataset.role='meal';let price=input(m?inputDec(m.price):'',{inputMode:'decimal',required:true});price.dataset.role='price';
 top.append(labelled('Refeição',meal),labelled('Preço por pessoa (R$)',price),button('Remover refeição','secondary',()=>box.remove()));
 let menu=el('div','planner-menu');(m?m.menu:[null]).forEach(f=>menu.append(menuRow(f)));
 box.append(top,menu,button('+ Adicionar função','secondary',()=>menu.append(menuRow(null))));return box}
function ruleRow(r){
 let row=el('div','planner-rule-row'),kind=select(RULES,r?r.kind:'exists_without');kind.dataset.role='kind';
 let when=input(r?r.when_tag:'',{placeholder:'sempre'});when.setAttribute('list','plannerTags');when.dataset.role='when';
 let fn=input(r?r.meal_function:'',{required:true});fn.setAttribute('list','plannerFunctions');fn.dataset.role='fn';
 let tag=input(r?r.tag:'');tag.setAttribute('list','plannerTags');tag.dataset.role='tag';
 let days=input(r&&r.days?r.days:'',{type:'number',min:1,max:30,step:1});days.dataset.role='days';
 const sync=()=>{let nr=kind.value==='no_repeat';days.disabled=!nr;days.required=nr;tag.required=!nr;tag.placeholder=nr?'qualquer ficha':''};kind.onchange=sync;sync();
 row.append(labelled('Regra',kind),labelled('Quando houver',when),labelled('Função',fn),labelled('Etiqueta',tag),labelled('Dias',days),button('Remover','secondary',()=>row.remove()));return row}
function openGroup(g){
 groupForm.reset();setFeedback(groupFormStatus,'','');$('groupId').value=g?g.id:'';$('groupName').value=g?g.name:'';$('groupActive').checked=g?g.active:true;groupForm.dataset.version=g?g.version:'';
 let meals=$('groupMeals'),rules=$('groupRules');meals.textContent='';rules.textContent='';(g?g.meals:[null]).forEach(m=>meals.append(mealBlock(m)));(g?g.rules:[]).forEach(r=>rules.append(ruleRow(r)));
 setCrudMode(groupForm,!!g,'Criar grupo','Atualizar grupo');$('groupDialogTitle').textContent=g?'Editar grupo':'Novo grupo';groupForm.hidden=false;groupDialog.showModal();$('groupName').focus()}
$('groupCancel').onclick=()=>{groupForm.hidden=true;groupDialog.close()};
$('groupAddMeal').onclick=()=>$('groupMeals').append(mealBlock(null));
$('groupAddRule').onclick=()=>$('groupRules').append(ruleRow(null));
groupForm.addEventListener('submit',async e=>{e.preventDefault();await runLocked(groupForm.querySelector('button[type="submit"]'),async()=>{
 if(!requireFields(groupForm,groupFormStatus))return;
 const v=(row,role)=>row.querySelector('[data-role="'+role+'"]').value;
 let meals=[...$('groupMeals').children].map(b=>({meal:v(b,'meal'),price:requestDecimal(v(b,'price')),menu:[...b.querySelectorAll('.planner-menu-row')].map(r=>({meal_function:v(r,'fn'),options:Number(v(r,'options')),per_capita:requestDecimal(v(r,'pc')),per_capita_unit:v(r,'unit'),adhesion:requestDecimal(v(r,'adh'))}))}));
 let rules=[...$('groupRules').children].map(r=>({kind:v(r,'kind'),when_tag:v(r,'when'),meal_function:v(r,'fn'),tag:v(r,'tag'),days:Number(v(r,'days')||0)}));
 let id=$('groupId').value,name=$('groupName').value.trim(),body={name,active:$('groupActive').checked,meals,rules};if(id)body.version=Number(groupForm.dataset.version);
 setFeedback(groupFormStatus,'loading','Salvando o grupo…');
 try{await api(id?'/api/customer-groups/'+id:'/api/customer-groups',{method:id?'PATCH':'POST',body:JSON.stringify(body)});groupForm.hidden=true;groupDialog.close();await loadAll();if(selectedPlan)await showPlan(selectedPlan);setFeedback(status,'success','Grupo '+name+(id?' atualizado.':' criado.'))}
 catch(err){setFeedback(groupFormStatus,'error',uiErrorMessage(err,'Não foi possível salvar o grupo. Revise os dados e tente de novo.'))}})});

if(canWrite){$('planNew').onclick=openPlan;$('groupNew').onclick=()=>openGroup(null)}
window.addEventListener('beforeunload',e=>{if(current&&dirty()){e.preventDefault();e.returnValue=''}});
await loadAll();
})();
