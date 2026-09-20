// Dependency-free DOM harness executes the actual production script with controlled fetch/timers.
const {test}=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs');
function setup(){
 const nodes=new Map(),pending=[],timers=new Map(),copies=[],downloads=[];let n=0,lastBlob;
 class Element{constructor(){this.children=[];this.disabled=false;this.hidden=false;this.style={setProperty(){}};this.classList={contains(){return false}}}replaceChildren(...v){this.children=v}append(...v){this.children.push(...v)}setAttribute(){}scrollIntoView(){}click(){if(this.download)downloads.push({name:this.download,blob:lastBlob});else return this.onclick?.()}}
 const el=id=>{if(!nodes.has(id))nodes.set(id,new Element());return nodes.get(id)};
 const launch=new Element();launch.dataset={business:'product'};
 const ctx={document:{getElementById:el,createElement:()=>new Element(),querySelectorAll:()=>[launch],querySelector:()=>new Element()},fetch:(url,opts)=>new Promise((resolve,reject)=>pending.push({url,opts,resolve:data=>resolve({ok:true,json:async()=>data}),reject})),setTimeout:fn=>{timers.set(++n,fn);return n},clearTimeout:id=>timers.delete(id),Date,Blob,AbortController,URL:{createObjectURL:b=>{lastBlob=b;return 'blob:test'},revokeObjectURL(){}},navigator:{clipboard:{writeText:async s=>copies.push(s)}}};vm.runInNewContext(fs.readFileSync(__dirname+'/commerce.js','utf8'),ctx);
 const flush=async()=>{for(let i=0;i<15;i++)await Promise.resolve()};
 const respond=async(url,data)=>{const i=pending.findIndex(x=>x.url===url);assert.ok(i>=0,'missing '+url);pending.splice(i,1)[0].resolve(data);await flush()};
 const run=(id,state='completed')=>({id,business:'product',state,start_at:'2026-09-20T00:00:00Z',requests:[{phase:'incident',status:200,duration_ms:1}],windows:{}});
 return {el,launch,pending,timers,copies,downloads,flush,respond,run};
}
test('A response cannot overwrite selected B; copy/download use B',async()=>{
 const h=setup();await h.respond('/demo/runs',{A:h.run('A'),B:h.run('B')});
 h.el('history').children[0].click();await h.flush();h.el('history').children[1].click();await h.flush();
 assert.equal(h.el('copy').disabled,true);assert.equal(h.el('download').disabled,true);
 await h.respond('/demo/runs/B',h.run('B'));await h.respond('/demo/runs',{});
 await h.respond('/demo/runs/A',h.run('A'));if(h.pending.some(x=>x.url==='/demo/runs'))await h.respond('/demo/runs',{});
 assert.match(h.el('context').textContent,/"run_id": "B"/);
 await h.el('copy').click();h.el('download').click();assert.match(h.copies[0],/"run_id": "B"/);assert.equal(h.downloads[0].name,'incident-B.json');assert.match(await h.downloads[0].blob.text(),/"run_id": "B"/);assert.equal(h.timers.size,0);
});
test('stale answer and failed read cannot change B',async()=>{
 const h=setup();await h.respond('/demo/runs',{A:h.run('A'),B:h.run('B')});h.el('history').children[0].click();await h.respond('/demo/runs/A',h.run('A'));await h.respond('/demo/runs',{A:h.run('A'),B:h.run('B')});
 h.el('reveal').click();h.el('history').children[1].click();await h.respond('/demo/runs/B',h.run('B'));await h.respond('/demo/runs',{});await h.respond('/demo/runs/A/answer',{answer:'A'});assert.equal(h.el('answer').hidden,true);assert.match(h.el('context').textContent,/"B"/);
});

test('new refresh wins same-run poll, stale rejection creates no timer',async()=>{
 const h=setup();await h.respond('/demo/runs',{A:h.run('A')});h.el('history').children[0].click();await h.flush();
 const old=h.pending.find(x=>x.url==='/demo/runs/A');h.pending.splice(h.pending.indexOf(old),1);
 h.el('refresh').click();await h.respond('/demo/runs',{});const fresh=h.run('A');fresh.requests[0].duration_ms=900;await h.respond('/demo/runs/A',fresh);await h.respond('/demo/runs',{});
 old.resolve(h.run('A'));await h.flush();assert.match(h.el('context').textContent,/900/);assert.equal(h.timers.size,0);
});
test('only one timer survives refresh and terminal stop ends polling',async()=>{
 const h=setup();await h.respond('/demo/runs',{A:h.run('A','incident')});h.el('history').children[0].click();await h.respond('/demo/runs/A',h.run('A','incident'));await h.respond('/demo/runs',{});assert.equal(h.timers.size,1);
 h.el('refresh').click();await h.respond('/demo/runs',{});await h.respond('/demo/runs/A',h.run('A','incident'));await h.respond('/demo/runs',{});assert.equal(h.timers.size,1);
 h.el('stop').click();await h.respond('/demo/runs/A/stop',{});await h.respond('/demo/runs/A',h.run('A','cancelled'));await h.respond('/demo/runs',{});assert.equal(h.timers.size,0);
});

test('late aborted read error cannot replace current status',async()=>{
 const h=setup();await h.respond('/demo/runs',{A:h.run('A'),B:h.run('B')});h.el('history').children[0].click();await h.flush();const old=h.pending.find(x=>x.url==='/demo/runs/A');h.pending.splice(h.pending.indexOf(old),1);
 h.el('history').children[1].click();await h.respond('/demo/runs/B',h.run('B'));await h.respond('/demo/runs',{});const status=h.el('status').textContent;old.reject(new Error('stale network failure'));await h.flush();assert.equal(h.el('status').textContent,status);assert.equal(h.timers.size,0);
});

 test('launch response cannot steal a selection made while POST was pending',async()=>{
 const h=setup();await h.respond('/demo/runs',{B:h.run('B')});h.launch.click();await h.flush();h.el('history').children[0].click();await h.respond('/demo/runs/B',h.run('B'));await h.respond('/demo/runs',{id:'new-run'});await h.respond('/demo/runs',{});assert.equal(h.pending.some(x=>x.url==='/demo/runs/new-run'),false);assert.match(h.el('context').textContent,/"run_id": "B"/);
 });
