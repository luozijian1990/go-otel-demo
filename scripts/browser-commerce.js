// Pass this async function to playwright-cli run-code after opening an isolated UI.
// Real business fetches and browser clipboard/downloads; no mocked responses.
async (page) => {
  const results=[];
  for(const url of ['http://localhost:18086/ui/','http://localhost:18083/']){
    await page.goto(url);
    await page.context().grantPermissions(['clipboard-read','clipboard-write'],{origin:url.split('/').slice(0,3).join('/')});
    const errors=[];const listener=e=>errors.push(e.message);page.on('pageerror',listener);
    await page.locator('.healthy[data-business="product"]').click();
    await page.waitForFunction(()=>document.getElementById('state').textContent==='已完成');
    const id=await page.locator('#context').evaluate(e=>JSON.parse(e.textContent).run_id);
    await page.locator('#copy').click();
    const copied=await page.evaluate(()=>navigator.clipboard.readText());
    if(!copied.includes('"run_id": "'+id+'"'))throw new Error('clipboard mismatch');
    const downloaded=page.waitForEvent('download');await page.locator('#download').click();const file=await downloaded;
    if(file.suggestedFilename()!=='incident-'+id+'.json')throw new Error('download name mismatch');
    const stream=await file.createReadStream();let content='';for await(const chunk of stream)content+=chunk.toString();
    if(JSON.parse(content).run_id!==id)throw new Error('download content mismatch');
    if(/"(target|action|receipt|expected_|scenario_id)"/.test(content))throw new Error('answer leaked');
    await page.locator('.healthy[data-business="inventory"]').click();
    await page.waitForFunction(previous=>document.getElementById('state').textContent==='已完成'&&JSON.parse(document.getElementById('context').textContent).run_id!==previous,id);
    const next=await page.locator('#context').evaluate(e=>JSON.parse(e.textContent).run_id);
    await page.locator('#history button').filter({hasText:id.slice(0,8)}).click();
    await page.waitForFunction(expected=>JSON.parse(document.getElementById('context').textContent||'{}').run_id===expected,id);
    await page.locator('#history button').filter({hasText:next.slice(0,8)}).click();
    await page.waitForFunction(expected=>JSON.parse(document.getElementById('context').textContent||'{}').run_id===expected,next);
    await page.locator('#copy').click();if(!(await page.evaluate(()=>navigator.clipboard.readText())).includes(next))throw new Error('history clipboard mismatch');
    page.off('pageerror',listener);if(errors.length)throw new Error(errors.join(';'));
    results.push({url,run_id:id,selected_after_switch:next,copy_download:'pass',page_errors:errors});
  }
  return {browser:page.context().browser().version(),results};
}
