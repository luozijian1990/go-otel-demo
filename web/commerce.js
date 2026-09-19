(() => {
  const el=id=>document.getElementById(id);
  const names={product:'商品浏览',inventory:'库存操作',order:'提交订单',payment:'确认支付'};
  const states={preparing:'准备',preflight:'预检',baseline:'正常流量',incident:'业务调用',recovery:'恢复观察',completed:'已完成',cancelled:'已停止',environment_unhealthy:'环境未就绪'};
  const terminal=new Set(['completed','cancelled','environment_unhealthy']);
  let selected=null, snapshot=null, timer=null, traceTimer=null, traceRun=null, traceTicket=0;
  async function api(path,body){const r=await fetch('/demo/'+path,body===undefined?{}:{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});let data;try{data=await r.json()}catch{throw new Error('服务正在准备或网关不可用，请稍后刷新')};if(!r.ok)throw new Error(data.error||'请求失败 '+r.status);return data;}
  const action=fn=>async()=>{try{await fn()}catch(e){el('status').textContent=e.message;await history().catch(()=>{})}};
  function context(run){return {business:names[run.business],run_id:run.id,start_at:run.start_at,end_at:run.end_at,windows:run.windows,observed_requests:run.requests.filter(r=>r.phase!=='preflight'),limitations:['仅为业务症状与观测信息，不包含注入目标或答案。','指标依赖时间窗口和样本量；健康 trace 可能不保留。']};}
  async function history(){const runs=await api('runs');const list=Object.values(runs).sort((a,b)=>b.start_at.localeCompare(a.start_at));el('history').replaceChildren();for(const run of list){const b=document.createElement('button');b.textContent=names[run.business]+' · '+run.id.slice(0,8)+' · '+(states[run.state]||run.state);b.setAttribute('aria-pressed',String(run.id===selected));b.onclick=action(()=>select(run.id));el('history').append(b)}const busy=list.some(r=>!terminal.has(r.state));document.querySelectorAll('.launch,.healthy').forEach(b=>b.disabled=busy);return list;}
  async function select(id){clearTimeout(timer);clearTimeout(traceTimer);traceTicket++;traceRun=null;selected=id;el('answer').hidden=true;el('trace-tree').replaceChildren();el('trace-status').textContent='等待业务请求完成，再查询实际 trace…';await poll();}
  function renderTrace(data){
    const spans=[...data.spans].sort((a,b)=>a.start_us-b.start_us),ids=new Set(spans.map(s=>s.span_id)),children=new Map();
    for(const span of spans){const parent=ids.has(span.parent_span_id)?span.parent_span_id:'';if(!children.has(parent))children.set(parent,[]);children.get(parent).push(span)}
    const seen=new Set();el('trace-tree').replaceChildren();
    function append(span,depth){if(seen.has(span.span_id))return;seen.add(span.span_id);const row=document.createElement('div');row.className='trace-node '+(span.is_error?'trace-error':'trace-ok');row.style.setProperty('--depth',Math.min(depth,9));
      const title=document.createElement('div');title.className='trace-node-title';const service=document.createElement('strong');service.textContent=span.driver?'演示客户端（独立 trace）':span.service;const timing=document.createElement('span');timing.textContent=span.duration_ms.toFixed(2)+' ms'+(span.http_status?' · HTTP '+span.http_status:'')+(span.is_error?' · ERROR':'');title.append(service,timing);row.append(title);
      const op=document.createElement('div');op.className='trace-operation';op.textContent=span.operation;row.append(op);
      if(span.parent_span_id&&!ids.has(span.parent_span_id)){const note=document.createElement('small');note.textContent='父 span 未返回，不能据此认定它是入口。';row.append(note)}
      if(span.exceptions.length||span.statement){const details=document.createElement('details');details.open=span.is_error;const summary=document.createElement('summary');summary.textContent='实际异常 / SQL';const pre=document.createElement('pre');pre.textContent=[span.statement,...span.exceptions].filter(Boolean).join('\n');details.append(summary,pre);row.append(details)}
      el('trace-tree').append(row);for(const child of children.get(span.span_id)||[])append(child,depth+1);
    }
    for(const root of children.get('')||[])append(root,0);for(const span of spans)if(!seen.has(span.span_id))append(span,0);
  }
  async function loadTrace(id,ticket,deadline){
    if(id!==selected||ticket!==traceTicket)return;
    try{const data=await api('runs/'+id+'/trace');if(id!==selected||ticket!==traceTicket)return;
      if(data.status==='available'){renderTrace(data);el('trace-status').textContent='TraceId '+data.trace_id+' · '+data.spans.length+' 个实际 spans'+(data.truncated?' · 已截断':'')+' · 尚可能有迟到 spans';el('trace-retry').disabled=false;return;}
      el('trace-status').textContent=data.status==='unavailable'?'Jaeger 暂不可用，正在有界重试…':'链路采集中，等待尾部采样和导出…';
    }catch(e){if(ticket!==traceTicket)return;el('trace-status').textContent=e.message;}
    if(Date.now()<deadline)traceTimer=setTimeout(()=>loadTrace(id,ticket,deadline),2000);else{el('trace-status').textContent='45 秒内未取得链路。可能延迟、未保留或后端不可用；不补画虚构节点，可稍后重新查询。';el('trace-retry').disabled=false;}
  }
  el('trace-retry').onclick=()=>{if(!selected)return;clearTimeout(traceTimer);el('trace-retry').disabled=true;loadTrace(selected,++traceTicket,Date.now()+45000)};
  async function poll(){const run=await api('runs/'+selected);snapshot=run;el('state').textContent=states[run.state]||run.state;el('status').textContent=names[run.business]+' · '+run.id+(run.state==='environment_unhealthy'?' · 正常预检失败，未继续注入故障':'');const requests=run.requests.filter(r=>r.phase!=='preflight');el('count').textContent=requests.length;el('failed').textContent=requests.filter(r=>r.status>=500||r.status===0).length;el('latency').textContent=requests.length?(Math.max(...requests.map(r=>r.duration_ms))/1000).toFixed(2)+'s':'—';el('phases').replaceChildren();for(const [phase,[start,end]]of Object.entries(run.windows)){const span=document.createElement('span');const ms=Date.parse(end)-Date.parse(start);span.textContent=(states[phase]||phase)+' '+(ms<1000?ms+' ms':(ms/1000).toFixed(1)+' s');el('phases').append(span)}el('requests').replaceChildren();for(const row of [...requests].reverse().slice(0,12)){const tr=document.createElement('tr');for(const text of [states[row.phase]||row.phase,row.status||'未完成',row.duration_ms+' ms']){const td=document.createElement('td');td.textContent=text;if(row.status>=500)td.className='error';tr.append(td)}const td=document.createElement('td');if(row.trace_id){const a=document.createElement('a');a.textContent=row.trace_id.slice(0,12)+' ↗';a.href='http://localhost:16686/trace/'+encodeURIComponent(row.trace_id);a.target='_blank';a.rel='noreferrer';a.title=row.trace_id;td.append(a)}else td.textContent='尚无 TraceId';tr.append(td);el('requests').append(tr)}el('context').textContent=JSON.stringify(context(run),null,2);el('copy').disabled=el('download').disabled=requests.length===0;el('stop').disabled=terminal.has(run.state);el('reveal').disabled=!terminal.has(run.state);if(requests.some(r=>r.trace_id)&&traceRun!==run.id){traceRun=run.id;el('trace-retry').disabled=true;loadTrace(run.id,traceTicket,Date.now()+45000)}await history();if(!terminal.has(run.state))timer=setTimeout(()=>poll().catch(e=>el('status').textContent=e.message),1500);}
  document.querySelectorAll('.launch,.healthy').forEach(button=>button.onclick=action(async()=>{document.querySelectorAll('.launch,.healthy').forEach(b=>b.disabled=true);const run=await api('runs',{business:button.dataset.business,traffic:el('traffic').value,mode:button.classList.contains('healthy')?'healthy':'fault'});await select(run.id);document.querySelector('.trace-panel').scrollIntoView({behavior:'smooth',block:'start'});}));
  el('stop').onclick=action(async()=>{await api('runs/'+selected+'/stop',{});await poll()});
  el('refresh').onclick=action(async()=>{await history();if(selected)await poll()});
  el('copy').onclick=action(async()=>{await navigator.clipboard.writeText('$aiops-incident-rootcause 请读取应用关系 reference，按需使用 Jaeger、Loki、Prometheus 三个 Skill，独立分析以下业务现场。不要读取故障目录或 answer 接口；给出根因、传播、业务影响和证据限制。\n'+JSON.stringify(context(snapshot),null,2));el('status').textContent='排障上下文已复制；在 AI 会话中粘贴即可开始调查。'});
  el('download').onclick=()=>{const url=URL.createObjectURL(new Blob([JSON.stringify(context(snapshot),null,2)],{type:'application/json'}));const a=document.createElement('a');a.href=url;a.download='incident-'+selected+'.json';a.click();URL.revokeObjectURL(url)};
  el('reveal').onclick=action(async()=>{el('answer').textContent=JSON.stringify(await api('runs/'+selected+'/answer'),null,2);el('answer').hidden=false});
  history().catch(e=>el('status').textContent=e.message);
})();
