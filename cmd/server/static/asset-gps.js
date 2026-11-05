// Patrimônio: GPS somente durante ações do operador; nenhuma coleta em segundo plano.
window.AssetGPS = (() => {
  const $ = id => document.getElementById(id);
  const pendingKey = 'pendingAssetGPSManual';
  const labels = {CREATE:'Cadastro',MOVE:'Movimentação',STATUS:'Alteração de estado',MANUAL:'Captura manual'};
  let selectedTag = '', page = 0, sequence = 0;

  function feedback(message, type='') {
    const node = $('assetGPSStatus');
    if(!node)return;
    node.className='operation-feedback'+(type?' '+type:'');
    node.textContent=message||'';
    node.hidden=!message;
  }

  function capture() {
    return new Promise(resolve => {
      if(!window.isSecureContext || !navigator.geolocation) {
        feedback('GPS indisponível. É necessário HTTPS e acesso à localização.','error');
        return resolve(null);
      }
      navigator.geolocation.getCurrentPosition(position => {
        const c=position.coords;
        if(![c.latitude,c.longitude,c.accuracy].every(Number.isFinite)) {
          feedback('O aparelho não forneceu uma posição válida.','error');
          return resolve(null);
        }
        resolve({latitude:c.latitude,longitude:c.longitude,accuracy_m:c.accuracy,
          captured_at:new Date(position.timestamp).toISOString()});
      },() => {
        feedback('Não foi possível obter a localização. Confira a permissão do GPS.','error');
        resolve(null);
      },{enableHighAccuracy:true,maximumAge:0,timeout:10000});
    });
  }

  async function refresh(more=false) {
    const current=$('assetGPSCurrent'),list=$('assetGPSHistory'),moreBtn=$('assetGPSMore');
    if(!current||!list)return;
    const requested=selectedTag, seq=++sequence;
    if(!more){page=0;list.textContent='';}
    if(moreBtn)moreBtn.hidden=true;
    if(!requested){current.textContent='Selecione um patrimônio para consultar a posição.';return;}
    try {
      const rows=await api('/api/assets/'+encodeURIComponent(requested)+'/gps?offset='+page*100);
      if(seq!==sequence||requested!==selectedTag)return;
      if(!more)current.textContent=rows.length
        ?'Última posição: '+rows[0].latitude.toFixed(6)+', '+rows[0].longitude.toFixed(6)+' · precisão ±'+Math.round(rows[0].accuracy_m)+' m'
        :'Nenhuma posição GPS registrada.';
      for(const p of rows) {
        const row=document.createElement('div');row.className='row';
        const detail=document.createElement('span');
        detail.textContent=new Date(p.captured_at).toLocaleString('pt-BR')+' · '+(labels[p.source]||p.source)+
          ' · '+p.user+' · precisão ±'+Math.round(p.accuracy_m)+' m';
        const link=document.createElement('a');
        link.href='https://www.google.com/maps?q='+encodeURIComponent(p.latitude+','+p.longitude);
        link.target='_blank';link.rel='noopener noreferrer';link.textContent='Ver no mapa';
        row.append(detail,link);list.append(row);
      }
      if(moreBtn)moreBtn.hidden=rows.length<100;
    } catch (_) {
      if(seq===sequence)feedback('Não foi possível consultar o histórico GPS.','error');
    }
  }

  function setTag(next) {
    selectedTag=next||'';
    feedback('');
    refresh();
  }

  function getPending() {
    try{return JSON.parse(sessionStorage.getItem(pendingKey)||'null')}catch(_){return null}
  }

  async function sendPending() {
    const pending=getPending();
    if(!pending)return;
    try {
      await api('/api/assets/'+encodeURIComponent(pending.tag)+'/gps',
        {method:'POST',body:JSON.stringify(pending.body),timeoutMs:15000});
      sessionStorage.removeItem(pendingKey);
      feedback('Localização registrada no histórico.','success');
      if(selectedTag===pending.tag)await refresh();
    } catch(e) {
      feedback('GPS pendente de confirmação. Use “Registrar posição atual” para tentar novamente.','error');
    }
  }

  async function manual() {
    if(!selectedTag){feedback('Selecione um patrimônio antes de registrar o GPS.','error');return;}
    const button=$('assetGPSManual');button.disabled=true;
    try {
      if(getPending()){if(getPending().tag!==selectedTag){feedback('Existe uma captura anterior pendente de outro patrimônio. Abra-o para reenviar.','error');return;}await sendPending();return;}
      const gps=await capture();
      if(!gps)return;
      sessionStorage.setItem(pendingKey,JSON.stringify({
        tag:selectedTag,body:{operation_id:newOperationID(),gps}
      }));
      await sendPending();
    }finally{button.disabled=false;}
  }

  document.addEventListener('DOMContentLoaded',()=>{
    $('assetGPSManual')?.addEventListener('click',manual);
    $('assetGPSMore')?.addEventListener('click',()=>{page++;refresh(true)});
    if(getPending())feedback('Existe uma captura anterior pendente. Selecione o patrimônio correspondente e toque em Registrar posição atual.','error');
  });

  return {setTag,capture,refresh};
})();
