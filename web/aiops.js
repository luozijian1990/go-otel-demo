(() => {
  const root = document.getElementById('aiops-lab');
  root.innerHTML = `<div class="card"><div class="card-header"><div><h2 class="card-title">随机故障实验室</h2><span class="subtitle">真实请求 · 独立证据 · 冻结后对照</span></div><span class="tag">AIOps / 01</span></div>
  <div class="card-body">
    <div class="lab-toolbar"><button id="lab-random">Random Error</button><button id="lab-healthy">健康对照</button>
    <label>模式 <select id="lab-mode"><option value="single">单次请求</option><option value="episode">持续实验 · 60 / 60 / 30 秒</option></select></label>
    <label>答案 <select id="lab-visibility"><option value="learning">教学</option><option value="blind">盲测</option></select></label><button id="lab-cancel" disabled>停止实验</button></div>
    <p id="lab-status" role="status" aria-live="polite">选择随机故障或健康对照开始。单次请求的指标通常不足以判断趋势。</p>
    <div id="lab-signals" class="lab-toolbar"></div><p id="lab-business"></p>
    <a id="lab-trace" class="button-link" hidden target="_blank" rel="noreferrer">业务 Trace → Jaeger</a>
    <div class="lab-columns"><div><h3>独立诊断</h3><div class="lab-toolbar"><select id="lab-view" aria-label="诊断证据范围"><option value="all">Traces + Logs + Metrics</option><option value="trace+logs">Traces + Logs</option><option value="trace-only">Trace only</option></select><button id="lab-analyze" disabled>新建诊断 revision</button></div>
    <p id="lab-analysis-status">未开始分析</p><div id="lab-diagnosis"></div>
    <div class="lab-toolbar"><button id="lab-copy" disabled>复制外部分析指令</button><button id="lab-download" disabled>下载证据 JSON</button></div>
    <details><summary>提交外部 Skill 结果</summary><label for="lab-result">DiagnosisResult JSON</label><textarea id="lab-result" rows="9" spellcheck="false"></textarea><button id="lab-submit" disabled>提交并冻结</button></details>
    </div><div><h3>真实答案与对照</h3><button id="lab-reveal" disabled>揭晓真实执行结果</button><pre id="lab-truth">盲测在诊断冻结后才能揭晓。</pre><pre id="lab-evaluation">尚无冻结结果</pre></div></div>
    <details><summary>证据索引与原始快照</summary><pre id="lab-evidence"></pre></details>
    <h3>实验历史</h3><div id="lab-history" class="lab-toolbar"></div><details><summary>评测汇总与排除项</summary><pre id="lab-summary"></pre></details>
  </div></div>`;
  const el = id => document.getElementById('lab-' + id);
  const api = '/aiops/api/v1';
  let run = null, analysis = null, bundle = null, timer = null;
  async function call(path, body) {
    const r = await fetch(api + path, body === undefined ? {} : {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
    const data = await r.json(); if (!r.ok) throw new Error(data.detail || r.status); return data;
  }
  const show = (id, data) => { el(id).textContent = typeof data === 'string' ? data : JSON.stringify(data,null,2); };
  const action = fn => async () => { try { await fn(); } catch(e) { show('status', e.message); el('random').disabled=el('healthy').disabled=false; } };
  async function history() {
    const rows = await call('/runs'); el('history').replaceChildren();
    rows.forEach(r => { const b=document.createElement('button');b.textContent=r.id.slice(0,8)+' · '+r.status;b.onclick=action(()=>select(r.id));el('history').append(b); });
    const busy=rows.some(r=>!['completed','cancelled','interrupted','injection_failed','environment_unhealthy'].includes(r.status)||r.analysis_state==='collecting');
    el('random').disabled=el('healthy').disabled=busy;
    show('summary', await call('/summary'));
  }
  async function select(id) {
    clearTimeout(timer);run=id;analysis=null;bundle=null;show('truth','尚未揭晓');show('evaluation','尚无冻结结果');show('diagnosis',''); await refresh();
  }
  async function refresh() {
    if (!run) return;
    const r = await call('/runs/'+run);
    show('status',r.status+' · '+r.id+' · 证据 '+(r.analysis_state||'等待业务执行'));
    const last=r.business_result;
    const lines=[`实际请求 ${r.request_count} 次（含预检） · 跳过 ${r.skipped_requests||0} 次`];
    if(r.actual_rps!=null)lines[0]+=` · 实测 ${r.actual_rps.toFixed(2)} RPS`;
    if(last)lines.push(`最近业务请求：HTTP ${last.status??'未完成'} · ${last.duration_ms.toFixed(1)} ms · ${last.phase}`);
    const phaseNames={baseline:'故障前',incident:'故障窗口',recovery:'恢复观察'};
    for(const [phase,[start,end]] of Object.entries(r.windows||{}))lines.push(`${phaseNames[phase]||phase}　${new Date(start*1000).toLocaleTimeString()} → ${new Date(end*1000).toLocaleTimeString()}　${(end-start).toFixed(1)} 秒`);
    show('business',lines.join('\n'));
    el('signals').replaceChildren();
    Object.entries(r.signals||{}).forEach(([name,s])=>{const tag=document.createElement('span');tag.className='tag';tag.textContent=name+' · '+s.status;tag.title=s.reason||'';el('signals').append(tag);});
    el('trace').hidden=!r.representative_trace_id;
    if(r.representative_trace_id)el('trace').href='http://localhost:16686/trace/'+encodeURIComponent(r.representative_trace_id);
    const active=!['completed','cancelled','interrupted','injection_failed','environment_unhealthy'].includes(r.status)||r.analysis_state==='collecting';
    el('random').disabled=el('healthy').disabled=active;el('cancel').disabled=!active;
    el('analyze').disabled=!r.evidence_ready;el('reveal').disabled=r.visibility==='blind'&&!r.analyses.some(a=>a.status==='completed');
    if(r.evidence_ready&&!bundle){bundle=await call('/runs/'+run+'/evidence');show('evidence',bundle);el('download').disabled=false;}
    analysis=r.analyses[0]||null;
    if(analysis){
      show('analysis-status',(analysis.status==='awaiting_external'?'等待外部 Skill · 未配置自动模型':analysis.status)+' · '+analysis.mode+' · revision '+analysis.revision);
      el('copy').disabled=false;el('submit').disabled=analysis.status!=='awaiting_external';
      if(analysis.result){show('diagnosis',analysis.result);show('evaluation',await call('/analyses/'+analysis.id+'/evaluation'));}
    }
    await history();
    if(active||analysis?.status==='analyzing')timer=setTimeout(()=>refresh().catch(e=>show('status',e.message)),2000);
  }
  for(const kind of ['random','healthy'])el(kind).onclick=action(async()=>{
    el('random').disabled=el('healthy').disabled=true;
    const r=await call('/runs/'+kind,{mode:el('mode').value,visibility:el('visibility').value});await select(r.id);
  });
  el('cancel').onclick=action(async()=>{await call('/runs/'+run+'/cancel',{});await refresh();});
  el('analyze').onclick=action(async()=>{await call('/runs/'+run+'/analyses',{mode:el('view').value});await refresh();});
  el('reveal').onclick=action(async()=>show('truth',await call('/runs/'+run+'/ground-truth')));
  el('submit').onclick=action(async()=>{await call('/analyses/'+analysis.id+'/result',JSON.parse(el('result').value));await refresh();});
  el('download').onclick=action(async()=>{
    const data=analysis?await call('/analyses/'+analysis.id+'/evidence'):bundle;
    const url=URL.createObjectURL(new Blob([JSON.stringify(data,null,2)],{type:'application/json'}));const a=document.createElement('a');a.href=url;a.download='incident-'+run+'.json';a.click();URL.revokeObjectURL(url);
  });
  el('copy').onclick=action(async()=>{await navigator.clipboard.writeText('使用 aiops-incident-rootcause，仅获取 '+location.origin+api+'/analyses/'+analysis.id+'/evidence 的净化证据，独立诊断。不得读取场景目录、注入参数或 ground-truth。输出 DiagnosisResult，并提交至 '+location.origin+api+'/analyses/'+analysis.id+'/result。结果来源 external，不代表严格黑盒盲测。');show('status','已复制外部分析指令');});
  history().catch(e=>show('status','实验 API 尚未就绪：'+e.message));
})();
