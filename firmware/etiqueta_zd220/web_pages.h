#pragma once

// Páginas web — identidade visual TotalFood
// (cores, raios, componentes e textos de docs/guia-de-estilo-frontend.md do
//  repositório totalfood-estoque).
//
// Montagem de cada página no firmware:
//   HEAD_A + título + HEAD_B + PAGE_x_A + COMMON_JS + PAGE_x_B

static const char HEAD_A[] = R"HTML(<!doctype html><html lang="pt-BR"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="theme-color" content="#4b9f05">
<link rel="icon" href="/logo.png"><title>)HTML";

static const char HEAD_B[] = R"HTML( · Etiquetas</title>
<style>
:root{
--brand-primary:#4b9f05;--brand-primary-hover:#3f8504;--brand-primary-active:#326a03;--brand-primary-soft:#edf7e8;
--text-primary:#171a1f;--text-secondary:#343a40;--text-muted:#667085;
--surface:#fff;--surface-secondary:#f6f7f4;--background:#fafbf7;--border:#d9ded3;
--success:#1f6f43;--warning:#9a6400;--danger:#9b1c1c;--info:#1f5f8b;
--focus-ring:#a8d78f;--success-border:#b8d9b8;--danger-border:#f0c0c0;--info-border:#b8c7d9;--warning-border:#e3c58d;
--success-soft:#edf7e8;--danger-soft:#fff5f5;--info-soft:#f4f8fb;--warning-soft:#fffaf0;
--radius-sm:6px;--radius-md:10px}
*{box-sizing:border-box}
body{margin:0;background:var(--background);color:var(--text-primary);font:16px/1.45 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
.top{position:sticky;top:0;z-index:5;display:flex;align-items:center;justify-content:space-between;gap:12px;
height:64px;padding:0 16px;background:var(--surface);border-bottom:1px solid var(--border)}
.brand{display:flex;align-items:center;gap:12px;color:inherit;text-decoration:none;min-width:0}
.brand img{display:block;height:40px;width:auto}
.brand-copy{display:flex;flex-direction:column;padding-left:12px;border-left:1px solid var(--border)}
.brand-copy strong{font-size:1rem;font-weight:700;line-height:1.2}
.brand-copy small{font-size:.8125rem;font-weight:500;line-height:1.2;color:var(--text-muted)}
@media(max-width:390px){.brand-copy small{display:none}}
main{max-width:720px;margin:0 auto;padding:24px 16px 32px}
@media(max-width:767px){main{padding:16px}}
h1{font-size:1.25rem;font-weight:700;line-height:1.25;margin:0 0 4px}
h2{font-size:.9375rem;font-weight:700;line-height:1.3;margin:0 0 12px}
.hint{font-size:.8125rem;color:var(--text-muted);margin:0 0 16px}
.panel{background:var(--surface);border:1px solid var(--border);border-radius:var(--radius-md);padding:16px;margin-bottom:16px}
@media(min-width:900px){.panel{padding:24px}}
label{display:flex;flex-direction:column;gap:6px;font-size:.8125rem;font-weight:600;color:var(--text-muted);margin:0 0 16px}
label.check{flex-direction:row;align-items:center;gap:10px;font-size:1rem;font-weight:500;color:var(--text-primary);min-height:44px}
label .hint{margin:0;font-weight:400}
input,textarea{width:100%;font:inherit;font-size:1rem;font-weight:400;color:var(--text-primary);background:var(--surface);
border:1px solid var(--border);border-radius:var(--radius-sm);min-height:48px;padding:10px 12px}
input[type=checkbox]{width:22px;height:22px;min-height:0;margin:0;accent-color:var(--brand-primary)}
input[type=file]{padding:10px}
input.qty{font-size:1.25rem;font-weight:600;max-width:160px}
textarea{font:13px/1.45 ui-monospace,Consolas,monospace;min-height:360px;resize:vertical}
input:focus,textarea:focus,button:focus-visible,a:focus-visible{outline:3px solid var(--focus-ring);outline-offset:1px}
button,.button{display:inline-flex;align-items:center;justify-content:center;gap:8px;min-height:48px;padding:10px 16px;
font:inherit;font-weight:700;border-radius:var(--radius-sm);border:1px solid var(--brand-primary);background:var(--brand-primary);
color:#fff;cursor:pointer;text-decoration:none}
button:hover,.button:hover{background:var(--brand-primary-hover);border-color:var(--brand-primary-hover)}
button:active,.button:active{background:var(--brand-primary-active)}
.secondary{background:var(--surface);color:var(--text-primary);border-color:var(--border)}
.secondary:hover{background:var(--surface-secondary);border-color:var(--border)}
.danger{background:var(--surface);color:var(--danger);border-color:var(--danger-border)}
.danger:hover{background:var(--danger-soft);border-color:var(--danger-border)}
button:disabled,button:disabled:hover{background:var(--surface-secondary);color:var(--text-muted);border-color:var(--border);cursor:default}
.block{width:100%}
.actions{display:flex;flex-wrap:wrap;gap:8px}
.fields{display:grid;gap:0 12px}
@media(min-width:600px){.fields{grid-template-columns:1fr 1fr}.fields .wide{grid-column:1/-1}}
.status{display:inline-flex;align-items:center;gap:6px;min-height:32px;padding:4px 10px;border-radius:999px;font-size:.8125rem;
font-weight:600;white-space:nowrap;border:1px solid var(--border);background:var(--surface-secondary);color:var(--text-secondary)}
.status::before{content:"";width:8px;height:8px;border-radius:50%;background:currentColor}
.status.ok{color:var(--success);background:var(--success-soft);border-color:var(--success-border)}
.status.warn{color:var(--warning);background:var(--warning-soft);border-color:var(--warning-border)}
.status.err{color:var(--danger);background:var(--danger-soft);border-color:var(--danger-border)}
.operation-feedback{margin:12px 0 0;padding:12px;border:1px solid var(--border);border-radius:var(--radius-sm);
font-size:.9375rem;background:var(--surface-secondary);color:var(--text-secondary)}
.operation-feedback.loading{background:var(--info-soft);border-color:var(--info-border);color:var(--info)}
.operation-feedback.success{background:var(--success-soft);border-color:var(--success-border);color:var(--success)}
.operation-feedback.warning{background:var(--warning-soft);border-color:var(--warning-border);color:var(--warning)}
.operation-feedback.error{background:var(--danger-soft);border-color:var(--danger-border);color:var(--danger);font-weight:600}
.links{display:flex;flex-wrap:wrap;justify-content:center;gap:4px 8px;margin-top:24px}
.text-action{display:inline-flex;align-items:center;min-height:44px;padding:0 8px;color:var(--brand-primary-active);font-weight:700;text-decoration:none}
.text-action:hover{text-decoration:underline}
.chips{display:flex;flex-wrap:wrap;gap:8px}
.chip{font:13px ui-monospace,Consolas,monospace;padding:4px 8px;border-radius:var(--radius-sm);background:var(--brand-primary-soft);color:var(--brand-primary-active)}
.meta{font-size:.8125rem;color:var(--text-muted);margin:12px 0 0}
progress{width:100%;height:10px;margin-top:12px;accent-color:var(--brand-primary)}
textarea.list-edit{font:inherit;font-size:1rem;line-height:1.5;min-height:180px}
.tabs{display:flex;gap:4px;padding:4px;margin:0 0 16px;background:var(--surface-secondary);border:1px solid var(--border);border-radius:var(--radius-md)}
.tabs .tab{flex:1;min-height:44px;padding:8px 12px;background:transparent;border:0;border-radius:var(--radius-sm);color:var(--text-secondary);font-weight:600}
.tabs .tab:hover{background:var(--surface);border:0}
.tabs .tab[aria-selected=true]{background:var(--surface);color:var(--brand-primary-active);font-weight:700;box-shadow:0 1px 2px rgba(16,24,40,.06)}
/* Abas de página (Imprimir etiqueta / Fila TotalFood): navegação sublinhada,
   diferente do seletor de tipo de etiqueta. */
.page-tabs{display:flex;margin:-4px 0 20px;border-bottom:1px solid var(--border)}
.page-tabs a{flex:1;display:flex;align-items:center;justify-content:center;min-height:48px;padding:8px 12px;margin-bottom:-1px;border-bottom:3px solid transparent;color:var(--text-secondary);font-weight:600;text-decoration:none;text-align:center}
.page-tabs a[aria-current=page]{color:var(--brand-primary-active);border-bottom-color:var(--brand-primary);font-weight:700}
[hidden]{display:none!important}
</style></head><body>
<header class="top">
  <a class="brand" href="/"><img src="/logo.png" alt="TotalFood" width="94" height="40">
  <span class="brand-copy"><strong>Etiquetas</strong><small>Qualidade todo dia!</small></span></a>
  <span id="st" class="status" role="status" aria-live="polite" hidden></span>
</header>
)HTML";

// JS comum: status da impressora no cabeçalho, avisos e envio de formulários
static const char COMMON_JS[] = R"JS(
const $=id=>document.getElementById(id);
function feedback(el,state,msg){el.hidden=!msg;el.className='operation-feedback'+(state?' '+state:'');el.textContent=msg||''}
async function post(url,data){
  try{const r=await fetch(url,{method:'POST',body:new URLSearchParams(data||{})});
    try{return await r.json()}catch(e){return{ok:false,msg:r.status===401?'Digite o usuário e a senha de administrador.':'Não foi possível concluir. Tente de novo.'}}
  }catch(e){return{ok:false,msg:'Sem conexão com o equipamento. Confira o Wi-Fi e tente de novo.'}}
}
const ST={pronta:['Impressora pronta','ok'],desconectada:['Impressora desconectada','err'],
sem_papel:['Sem etiquetas','warn'],pausada:['Impressora pausada','warn'],cabeca_aberta:['Tampa aberta','warn']};
async function status(){
  const el=$('st');
  try{const j=await (await fetch('/api/status')).json();
    const s=j.updating?['Atualizando o sistema','warn']:(ST[j.printer]||[j.printer,'warn']);
    el.textContent=s[0];el.className='status '+s[1];el.hidden=false;
    if(window.onStatus)onStatus(j);
  }catch(e){el.textContent='Sem conexão';el.className='status err';el.hidden=false}
}
status();setInterval(()=>{if(!document.hidden)status()},5000);
document.addEventListener('visibilitychange',()=>{if(!document.hidden)status()});
)JS";

// ----------------------------------------------------------------------------
//  Imprimir
// ----------------------------------------------------------------------------
static const char PAGE_INDEX_A[] = R"HTML(
<main>
<nav class="page-tabs" aria-label="Páginas"><a href="/" aria-current="page">Imprimir etiqueta</a><a href="/fila">Fila TotalFood</a></nav>
<h1>Imprimir etiqueta</h1>
<p id="sub" class="hint">Preencha os dados do produto e escolha a quantidade.</p>
<div class="tabs" role="tablist" aria-label="Tipo de etiqueta">
  <button type="button" class="tab" role="tab" data-tipo="camara" aria-selected="true">Câmara fria</button>
  <button type="button" class="tab" role="tab" data-tipo="despacho" aria-selected="false">Despacho</button>
</div>
<form id="f" autocomplete="off">
  <section class="panel">
    <div id="fields" class="fields"><p class="hint">Carregando campos…</p></div>
  </section>
  <section class="panel">
    <label>Quantidade<input id="copias" name="copias" class="qty" type="number" min="1" max="999" value="1" inputmode="numeric" required></label>
    <button id="go" class="block">Imprimir</button>
    <p id="fb" class="operation-feedback" role="status" aria-live="polite" hidden></p>
  </section>
</form>
<nav class="links" aria-label="Administração">
  <a class="text-action" href="/config">Configurações</a>
  <a class="text-action" href="/template">Modelo da etiqueta</a>
  <a class="text-action" href="/update">Atualizar sistema</a>
</nav>
</main>
<script>)HTML";

static const char PAGE_INDEX_B[] = R"HTML(
const LABELS={produto:'Produto',val_original:'Validade original',manipulacao:'Manipulação',validade:'Validade',
peso_liq:'Peso líquido',lote:'Lote',sif:'SIF',responsavel:'Responsável',
hora_manipulacao:'Hora da manipulação',hora_validade:'Hora da validade',destino:'Destino'};
const HINTS={peso_liq:'Exemplo: 1,000 KG',manipulacao:'Preenchida com a data de hoje.',
hora_manipulacao:'Preenchida com a hora atual.',destino:'Escolha da lista ou digite um destino novo.'};
const SUBS={camara:'Preencha os dados do produto e escolha a quantidade.',
despacho:'Etiqueta de despacho: destino, produto em destaque, manipulação, validade e peso.'};
const WIDE=['produto','responsavel','destino'];
const DATES=['val_original','manipulacao','validade'];
const TODAY=['manipulacao'];
const NOW=['hora_manipulacao'];
const isDate=n=>DATES.includes(n)||n.startsWith('data');
const isTime=n=>n.startsWith('hora');
const nowHM=()=>{const d=new Date();return pad(d.getHours())+':'+pad(d.getMinutes())};
let tipo='camara';
try{const t=localStorage.getItem('tipo');if(t==='camara'||t==='despacho')tipo=t}catch(e){}
const label=n=>LABELS[n]||n.replace(/_/g,' ').replace(/^./,c=>c.toUpperCase());
const pad=n=>String(n).padStart(2,'0');
const todayISO=()=>{const d=new Date();return d.getFullYear()+'-'+pad(d.getMonth()+1)+'-'+pad(d.getDate())};
const toISO=b=>{const m=/^(\d{2})\/(\d{2})\/(\d{4})$/.exec(b||'');return m?m[3]+'-'+m[2]+'-'+m[1]:''};
const toBR=i=>{const m=/^(\d{4})-(\d{2})-(\d{2})$/.exec(i||'');return m?m[3]+'/'+m[2]+'/'+m[1]:i};

function selectTab(t){
  tipo=t;
  try{localStorage.setItem('tipo',t)}catch(e){}
  document.querySelectorAll('.tab').forEach(b=>b.setAttribute('aria-selected',b.dataset.tipo===t?'true':'false'));
  $('sub').textContent=SUBS[t];
  feedback($('fb'),'','');
  load();
}
document.querySelectorAll('.tab').forEach(b=>b.addEventListener('click',()=>selectTab(b.dataset.tipo)));

async function load(){
  const box=$('fields');
  box.innerHTML='<p class="hint">Carregando campos…</p>';
  try{
    const j=await (await fetch('/api/fields?tipo='+tipo)).json();
    box.innerHTML='';
    if(!j.fields.length){box.innerHTML='<p class="hint">O modelo não tem campos para preencher. Ajuste o modelo em Modelo da etiqueta.</p>'}
    for(const f of j.fields){
      const l=document.createElement('label');if(WIDE.includes(f.n))l.className='wide';
      l.append(label(f.n));
      const i=document.createElement('input');i.name=f.n;
      if(isDate(f.n)){i.type='date';i.value=TODAY.includes(f.n)?todayISO():toISO(f.v)}
      else if(isTime(f.n)){i.type='time';i.value=NOW.includes(f.n)?nowHM():(f.v||nowHM())}
      else{i.type='text';i.value=f.v||''}
      l.append(i);
      if(f.n==='destino'){                       // sugestões da lista de destinos
        const dl=document.createElement('datalist');dl.id='dl-destinos';i.setAttribute('list',dl.id);l.append(dl);
        fetch('/api/destinos').then(r=>r.json()).then(d=>{for(const v of d.destinos){const o=document.createElement('option');o.value=v;dl.append(o)}}).catch(()=>{});
      }
      if(HINTS[f.n]){const h=document.createElement('span');h.className='hint';h.textContent=HINTS[f.n];l.append(h)}
      box.append(l);
    }
    $('copias').value=j.copias||1;
  }catch(e){box.innerHTML='<p class="hint">Não foi possível carregar os campos. Confira a conexão e recarregue a página.</p>'}
}

$('f').addEventListener('submit',async ev=>{
  ev.preventDefault();
  const b=$('go'),fb=$('fb');b.disabled=true;b.textContent='Enviando…';feedback(fb,'loading','Enviando para a impressora…');
  const d={tipo};
  for(const i of ev.target.querySelectorAll('input'))d[i.name]=i.type==='date'?toBR(i.value):i.value;
  const j=await post('/api/print',d);
  feedback(fb,j.ok?(j.msg.includes('Atenção')?'warning':'success'):'error',j.msg||'Não foi possível imprimir. Tente de novo.');
  b.disabled=false;b.textContent='Imprimir';status();
});

selectTab(tipo);
</script></body></html>)HTML";

// ----------------------------------------------------------------------------
//  Fila TotalFood (aba própria, sem senha: rede interna fechada)
// ----------------------------------------------------------------------------
static const char PAGE_QUEUE_A[] = R"HTML(
<main>
<nav class="page-tabs" aria-label="Páginas"><a href="/">Imprimir etiqueta</a><a href="/fila" aria-current="page">Fila TotalFood</a></nav>
<h1>Fila TotalFood</h1>
<p class="hint">Etiquetas lançadas no estoque (caixas e testes) ficam salvas no servidor. Elas só são impressas quando alguém aperta o botão abaixo.</p>
<section class="panel" aria-labelledby="filaTitle">
  <h2 id="filaTitle">Buscar e imprimir</h2>
  <p class="hint">Imprime, em ordem, todas as etiquetas pendentes desta impressora.</p>
  <button id="wmsPull" type="button" class="block">Buscar e imprimir etiquetas</button>
  <p id="wmsFeedback" class="operation-feedback" role="status" aria-live="polite" hidden></p>
</section>
<nav class="links" aria-label="Administração">
  <a class="text-action" href="/config">Configurações</a>
</nav>
</main>
<script>)HTML";

static const char PAGE_QUEUE_B[] = R"HTML(
let wmsTimer=0;
async function wmsProgress(){
  try{
    const j=await(await fetch('/api/queue/status',{cache:'no-store'})).json();
    feedback($('wmsFeedback'),j.busy?'loading':'success',j.message+(j.printed?' · '+j.printed+' enviada(s)':''));
    if(j.busy)wmsTimer=setTimeout(wmsProgress,1200);
    else{$('wmsPull').disabled=false;status();}
  }catch(e){$('wmsPull').disabled=false;feedback($('wmsFeedback'),'error','Não foi possível consultar o ESP32.')}
}
$('wmsPull').addEventListener('click',async()=>{
  if(wmsTimer)clearTimeout(wmsTimer);
  $('wmsPull').disabled=true;
  feedback($('wmsFeedback'),'loading','Iniciando consulta manual...');
  const r=await post('/api/queue/pull',{});
  if(!r.ok){$('wmsPull').disabled=false;feedback($('wmsFeedback'),'error',r.msg);return;}
  wmsProgress();
});
// Se a fila já estiver em andamento (ex.: página recarregada), acompanha.
fetch('/api/queue/status',{cache:'no-store'}).then(r=>r.json()).then(j=>{
  if(j.busy){$('wmsPull').disabled=true;wmsProgress();}
}).catch(()=>{});
</script></body></html>)HTML";

// ----------------------------------------------------------------------------
//  Configurações
// ----------------------------------------------------------------------------
static const char PAGE_CONFIG_A[] = R"HTML(
<main>
<h1>Configurações</h1>
<p class="hint">Rede, senha e manutenção da impressora.</p>
<p id="warn" class="operation-feedback warning" hidden>A senha de administrador ainda é a padrão. Digite uma senha nova em Senha de administrador.</p>
<form id="f">
<section class="panel"><h2>Rede</h2>
  <label>Nome de acesso
    <input id="host" name="host" required maxlength="31" autocapitalize="off" spellcheck="false">
    <span class="hint">Endereço: <b>http://<span id="hp">etiqueta</span>.local</b>. Use letras minúsculas, números e hífen.</span></label>
  <label class="check"><input type="checkbox" id="raw9100" name="raw9100" value="1">Impressão direta pelo computador (porta 9100)</label>
</section>
<section class="panel"><h2>Senha de administrador</h2>
  <label>Senha nova
    <input id="admin_pass" name="admin_pass" type="password" minlength="6" autocomplete="new-password">
    <span class="hint">Usuário: admin. Deixe em branco para manter a senha atual. Vale para configurações, modelo e atualização.</span></label>
</section>
<section class="panel"><h2>Integração MQTT</h2>
  <label class="check"><input type="checkbox" id="mqtt_en" name="mqtt_en" value="1">Receber impressões por MQTT</label>
  <div class="fields">
    <label>Servidor<input id="mqtt_host" name="mqtt_host" placeholder="192.168.1.10" autocapitalize="off"></label>
    <label>Porta<input id="mqtt_port" name="mqtt_port" type="number" min="1" max="65535"></label>
    <label>Usuário<input id="mqtt_user" name="mqtt_user" autocapitalize="off"></label>
    <label>Senha<input id="mqtt_pass" name="mqtt_pass" type="password" autocomplete="new-password"><span class="hint">Deixe em branco para manter.</span></label>
    <label class="wide">Identificação<input id="dev_id" name="dev_id" autocapitalize="off"><span class="hint">Tópicos: zebra/identificação/print, /response e /status.</span></label>
  </div>
</section>
<section class="panel"><h2>Fila de etiquetas do TotalFood</h2>
  <p class="hint">A busca será manual, pelo botão na página inicial. Sem MQTT ou consulta periódica ao Render.</p>
  <label>Endereço HTTPS do sistema
    <input id="wms_url" name="wms_url" type="url" placeholder="https://seu-wms.onrender.com" autocapitalize="off" autocomplete="off">
  </label>
  <label>Token da impressora (gerado no WMS)
    <input id="wms_token" name="wms_token" type="password" autocomplete="new-password" placeholder="Deixe vazio para manter o atual">
    <span id="wmsPaired" class="hint">Ainda não pareado.</span>
  </label>
</section>
<button class="block">Salvar e reiniciar</button>
<p id="fb" class="operation-feedback" role="status" aria-live="polite" hidden></p>
</form>
<section class="panel" style="margin-top:24px"><h2>Confirmação pendente da fila</h2>
  <p class="hint">Use só se a página inicial avisar que o WMS recusou ou não recebeu a confirmação da última etiqueta. Antes, confira no WMS se ela saiu.</p>
  <button type="button" class="secondary" id="wmsDiscard">Descartar confirmação pendente</button>
  <p id="fbDiscard" class="operation-feedback" role="status" aria-live="polite" hidden></p>
</section>
<section class="panel"><h2>Destinos do despacho</h2>
  <label>Destinos sugeridos
    <textarea id="destinos" class="list-edit" spellcheck="false"></textarea>
    <span class="hint">Um destino por linha. Aparecem como sugestão no campo Destino. Destinos novos digitados na impressão entram aqui sozinhos.</span></label>
  <button type="button" class="secondary" id="saveDest">Salvar destinos</button>
  <p id="fbDest" class="operation-feedback" role="status" aria-live="polite" hidden></p>
</section>
<section class="panel"><h2>Impressora</h2>
  <div class="actions">
    <button class="secondary" data-cmd="calibrar">Calibrar etiquetas</button>
    <button class="secondary" data-cmd="config">Imprimir relatório da impressora</button>
  </div>
  <p id="fbCmd" class="operation-feedback" role="status" aria-live="polite" hidden></p>
  <p id="info" class="meta"></p>
</section>
<section class="panel"><h2>Equipamento</h2>
  <div class="actions">
    <a class="button secondary" href="/update">Atualizar sistema</a>
    <button class="secondary" id="restart">Reiniciar</button>
    <button class="danger" id="wifireset">Configurar outro Wi-Fi</button>
  </div>
  <p id="fbSys" class="operation-feedback" role="status" aria-live="polite" hidden></p>
</section>
<nav class="links"><a class="text-action" href="/">Voltar para impressão</a></nav>
</main>
<script>)HTML";

static const char PAGE_CONFIG_B[] = R"HTML(
async function load(){
  try{
    const c=await (await fetch('/api/config')).json();
    for(const k of ['host','mqtt_host','mqtt_port','mqtt_user','dev_id','wms_url'])$(k).value=c[k]||'';
    $('wmsPaired').textContent=c.wms_paired?'Token já configurado. Deixe o campo vazio para manter.':'Informe o token gerado no WMS.';
    $('raw9100').checked=c.raw9100;$('mqtt_en').checked=c.mqtt_en;$('hp').textContent=c.host;
    $('warn').hidden=!c.default_pass;
  }catch(e){feedback($('fb'),'error','Não foi possível carregar as configurações. Recarregue a página.')}
  try{const d=await (await fetch('/api/destinos')).json();$('destinos').value=d.destinos.join('\n')}
  catch(e){feedback($('fbDest'),'error','Não foi possível carregar os destinos. Recarregue a página.')}
}
$('wmsDiscard').addEventListener('click',async()=>{
  if(!confirm('Descartar a confirmação pendente? A etiqueta não será reimpressa.'))return;
  feedback($('fbDiscard'),'loading','Descartando…');
  const j=await post('/api/queue/discard',{});feedback($('fbDiscard'),j.ok?'success':'error',j.msg)});
$('saveDest').addEventListener('click',async()=>{
  feedback($('fbDest'),'loading','Salvando…');
  const j=await post('/api/destinos',{lista:$('destinos').value});feedback($('fbDest'),j.ok?'success':'error',j.msg)});
window.onStatus=s=>{
  $('info').textContent='Impressora '+s.vidpid+' · IP '+s.ip+' · Wi-Fi '+s.rssi+' dBm · versão '+s.fw+(s.ota_pending?' (em teste)':'');
};
$('host').addEventListener('input',e=>$('hp').textContent=e.target.value||'…');
$('f').addEventListener('submit',async e=>{
  e.preventDefault();const d={};
  for(const i of e.target.querySelectorAll('input'))if(i.type!=='checkbox'||i.checked)d[i.name]=i.value;
  feedback($('fb'),'loading','Salvando…');
  const j=await post('/api/config',d);feedback($('fb'),j.ok?'success':'error',j.msg);
  if(j.ok)setTimeout(()=>location.href='http://'+$('host').value+'.local/config',10000);
});
document.querySelectorAll('[data-cmd]').forEach(b=>b.addEventListener('click',async()=>{
  feedback($('fbCmd'),'loading','Enviando…');
  const j=await post('/api/command',{c:b.dataset.cmd});feedback($('fbCmd'),j.ok?'success':'error',j.msg)}));
$('restart').addEventListener('click',async()=>{
  const j=await post('/api/restart');feedback($('fbSys'),j.ok?'success':'error',j.msg);
  if(j.ok)setTimeout(()=>location.reload(),15000)});
$('wifireset').addEventListener('click',async()=>{
  if(!confirm('Apagar o Wi-Fi salvo? O equipamento vai criar a rede Etiqueta-Setup para configurar outro Wi-Fi.'))return;
  const j=await post('/api/wifireset');feedback($('fbSys'),j.ok?'success':'error',j.msg)});
load();
</script></body></html>)HTML";

// ----------------------------------------------------------------------------
//  Modelo da etiqueta
// ----------------------------------------------------------------------------
static const char PAGE_TEMPLATE_A[] = R"HTML(
<main>
<h1>Modelo da etiqueta</h1>
<p class="hint">Marque os dados que mudam a cada impressão como %nome%. A quantidade vem da tela de impressão.</p>
<div class="tabs" role="tablist" aria-label="Tipo de etiqueta">
  <button type="button" class="tab" role="tab" data-tipo="camara" aria-selected="true">Câmara fria</button>
  <button type="button" class="tab" role="tab" data-tipo="despacho" aria-selected="false">Despacho</button>
</div>
<section class="panel"><h2>Campos encontrados</h2><div id="chips" class="chips"></div>
<p id="auto" class="meta" hidden>Os campos %produto_y%, %produto_h%, %produto_w%, %produto_linhas%, %destino_y%, %destino_h% e %destino_w% são calculados automaticamente pelo tamanho do nome do produto e do destino.</p></section>
<section class="panel">
  <label>Código ZPL<textarea id="z" spellcheck="false" autocapitalize="off"></textarea></label>
  <div class="actions">
    <button id="save">Salvar modelo</button>
    <button id="reset" class="danger">Restaurar modelo padrão</button>
  </div>
  <p id="fb" class="operation-feedback" role="status" aria-live="polite" hidden></p>
</section>
<nav class="links"><a class="text-action" href="/">Voltar para impressão</a></nav>
</main>
<script>)HTML";

static const char PAGE_TEMPLATE_B[] = R"HTML(
const z=$('z');
const AUTO=['produto_y','produto_h','produto_w','produto_linhas','destino_y','destino_h','destino_w'];
let tipo='camara';
try{const t=localStorage.getItem('tipo');if(t==='camara'||t==='despacho')tipo=t}catch(e){}
function selectTab(t){
  tipo=t;
  try{localStorage.setItem('tipo',t)}catch(e){}
  document.querySelectorAll('.tab').forEach(b=>b.setAttribute('aria-selected',b.dataset.tipo===t?'true':'false'));
  feedback($('fb'),'','');
  load();
}
document.querySelectorAll('.tab').forEach(b=>b.addEventListener('click',()=>selectTab(b.dataset.tipo)));
function chips(){
  const s=new Set();let auto=false;
  for(const m of z.value.matchAll(/%([A-Za-z0-9_]{1,32})%/g)){if(AUTO.includes(m[1]))auto=true;else s.add(m[1])}
  $('auto').hidden=!auto;
  const c=$('chips');c.innerHTML='';
  if(!s.size){const p=document.createElement('p');p.className='hint';p.textContent='Nenhum campo encontrado. Marque os dados variáveis como %nome%.';c.append(p);return}
  for(const n of s){const e=document.createElement('span');e.className='chip';e.textContent='%'+n+'%';c.append(e)}
}
async function load(){
  z.value='';
  try{z.value=await (await fetch('/api/template?tipo='+tipo)).text();chips()}
  catch(e){feedback($('fb'),'error','Não foi possível carregar o modelo. Recarregue a página.')}
}
z.addEventListener('input',chips);
$('save').addEventListener('click',async()=>{
  feedback($('fb'),'loading','Salvando…');
  const j=await post('/api/template',{tipo,zpl:z.value});feedback($('fb'),j.ok?'success':'error',j.msg)});
$('reset').addEventListener('click',async()=>{
  const nome=tipo==='despacho'?'Despacho':'Câmara fria';
  if(!confirm('Substituir o modelo '+nome+' pelo modelo padrão? As alterações feitas aqui serão perdidas.'))return;
  const j=await post('/api/template/reset',{tipo});feedback($('fb'),j.ok?'success':'error',j.msg);if(j.ok)load()});
selectTab(tipo);
</script></body></html>)HTML";

// ----------------------------------------------------------------------------
//  Atualizar sistema
// ----------------------------------------------------------------------------
static const char PAGE_UPDATE_A[] = R"HTML(
<main>
<h1>Atualizar sistema</h1>
<p class="hint">Escolha o arquivo .bin gerado pela Arduino IDE em Sketch › Exportar binário compilado.</p>
<section class="panel">
  <p id="ver" class="meta" style="margin:0 0 16px"></p>
  <form id="f">
    <label>Arquivo da atualização<input id="bin" type="file" accept=".bin" required></label>
    <button id="go" class="block">Enviar atualização</button>
    <progress id="pg" max="100" value="0" hidden></progress>
    <p id="fb" class="operation-feedback" role="status" aria-live="polite" hidden></p>
  </form>
  <p class="meta">As configurações, o Wi-Fi e o modelo da etiqueta são mantidos. Se a versão nova não funcionar, o equipamento volta sozinho para a versão anterior.</p>
</section>
<nav class="links"><a class="text-action" href="/config">Voltar para configurações</a></nav>
</main>
<script>)HTML";

static const char PAGE_UPDATE_B[] = R"HTML(
window.onStatus=s=>{
  $('ver').textContent='Versão instalada: '+s.fw+(s.ota_pending?' (em teste, confirmada após 1 minuto funcionando)':'');
  if(!s.idle&&!$('go').dataset.busy)feedback($('fb'),'warning','A impressora está imprimindo. Aguarde terminar para atualizar.');
};
$('f').addEventListener('submit',e=>{
  e.preventDefault();
  const f=$('bin').files[0],fb=$('fb'),b=$('go'),pg=$('pg');
  if(!f||!f.name.toLowerCase().endsWith('.bin')){feedback(fb,'error','Escolha um arquivo .bin.');return}
  if(!confirm('Atualizar o sistema com '+f.name+'? A impressão fica indisponível por cerca de 30 segundos.'))return;
  const fd=new FormData();fd.append('firmware',f,f.name);
  const x=new XMLHttpRequest();x.open('POST','/api/update');
  b.disabled=true;b.dataset.busy='1';b.textContent='Enviando…';pg.hidden=false;pg.value=0;
  feedback(fb,'loading','Enviando a atualização. Não feche esta página.');
  x.upload.onprogress=ev=>{if(ev.lengthComputable)pg.value=ev.loaded/ev.total*100};
  x.onload=()=>{
    let j;try{j=JSON.parse(x.responseText)}catch(err){j={ok:false,msg:'Não foi possível concluir a atualização. Tente de novo.'}}
    feedback(fb,j.ok?'success':'error',j.msg);
    if(j.ok){b.textContent='Reiniciando…';setTimeout(()=>location.href='/',25000)}
    else{b.disabled=false;delete b.dataset.busy;b.textContent='Enviar atualização';pg.hidden=true}
  };
  x.onerror=()=>{feedback(fb,'error','A conexão caiu durante o envio. Confira o Wi-Fi e tente de novo.');
    b.disabled=false;delete b.dataset.busy;b.textContent='Enviar atualização';pg.hidden=true};
  x.send(fd);
});
</script></body></html>)HTML";
