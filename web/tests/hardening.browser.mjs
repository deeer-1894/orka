import { before, after, test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'vite';
import { chromium } from 'playwright';
import { fileURLToPath } from 'node:url';
let server, browser, origin;
const root = fileURLToPath(new URL('../', import.meta.url));
const BASE = '/api/v1/controller';
before(async () => { server = await createServer({ root, optimizeDeps: { include: ['xlsx'] }, server: { port: 0, host: '127.0.0.1' } }); await server.listen(); origin = `http://127.0.0.1:${server.httpServer.address().port}`; browser = await chromium.launch({ headless: true, args: ['--no-sandbox'] }); });
after(async () => { await browser?.close(); await server?.close(); });
const msg = (id, type, content = '', extra = {}) => ({ id, type, role: type === 'chat' ? 'user' : '', content, ts: Number(id.replace(/\D/g, '')) || 1, meta: { conversation_id: 'a', run_id: 'run-a', trace_id: 'trace-a' }, ...extra });
const record = (extra = {}) => ({ run_id: 'run-a', conversation_id: 'a', trace_id: 'trace-a', status: 'partial', resumable: true, prompt: 'original', unfinished: ['finish report'], tokens: 12, tool_calls: 2, created_at: 10, ...extra });
async function app(t, options = {}) {
 const context = await browser.newContext({ viewport: {width:1440,height:1000}, serviceWorkers:'block' }); t.after(()=>context.close());
 await context.addInitScript(()=>localStorage.setItem('orka.token','isolated-hardening-test'));
 const requests=[], errors=[]; let modelReads=0;
 const config={provider:'custom',base_url:'https://example.invalid/v1',api_key_set:false,models:['model-a'],enabled:true};
 await context.route('**/*', async route=>{
  const req=route.request(), url=new URL(req.url());
  if(url.origin !== origin) return route.fulfill({body:'',contentType:'text/css'});
  if(!url.pathname.startsWith('/api/')) return route.continue();
  const path=url.pathname.slice(BASE.length), body=req.postDataJSON()||{}; requests.push({path,body,query:Object.fromEntries(url.searchParams)});
  const json=data=>route.fulfill({json:{code:0,data}});
  if(options.handle && await options.handle({path,body,route,json,url,requests})) return;
  switch(path){
   case '/auth/me': return json({email:'test@example.invalid',name:'Test'});
   case '/conversation/list': return json(['a','b'].map(id=>({conversation_id:id,title:`Conversation ${id}`,owner_email:'test@example.invalid',created_at:1})));
   case '/conversation/shared-with-me': return json([]);
   case '/conversation/get-messages': return json(body.conversation_id==='a' ? options.history||[] : []);
   case '/conversation/create-conversation': return json({conversation_id:'new',title:'New',owner_email:'test@example.invalid'});
   case '/models': modelReads++; return json(options.emptyModels && modelReads>1 ? [] : [{version:'auto',label:'Auto',hint:''},{version:'model-a',label:'model-a',hint:''}]);
   case '/model-settings/get': return options.configFails ? route.fulfill({status:503,json:{code:503,msg:'configuration unavailable'}}) : json(config);
   case '/model-settings/save': return json({...config,...body});
   case '/task/get-tasks': return json({tasks:[]});
   case '/skill/list': return json({skills:[]});
   case '/notification/list': return json({notifications:[],unread:0});
   case '/system/status': return json({version:'fixture',build_time:'',started_at:'2026-09-15T10:00:00Z',ready:true,services:[],model_probe:'not_run'});
   case '/metrics': return json({total_tokens:0});
   case '/delivery/list': return json({deliveries:[]});
   case '/artifact/list': return json({artifacts:[]});
   case '/artifact/by-conversation': return route.fulfill({status:404,json:{code:404}});
   case '/file/list': return json(body.path==='.' ? [{name:'reports',dir:true,size:0}] : [{name:'deep.txt',dir:false,size:3}]);
   case '/file/upload-chunk': return json({});
   case '/run/list': return json({runs: options.runs||[]});
   case '/run/get': return json((options.runs||[]).find(r=>r.run_id===body.run_id));
   case '/chat/resume_run': return json({resumed:false,conversation_id:'a'});
   case '/events': return route.fulfill({status:503,body:''});
   case '/chat/kill': return route.fulfill({status:503,json:{code:503,msg:'stop unavailable'}});
   case '/chat/attach': return route.fulfill({status:404,body:''});
   case '/chat/followups': return json({suggestions:['Follow up once']});
   case '/chat/run': return route.fulfill({status:503,body:''});
   default:
    if (/^\/run\/[^/]+\/budget$/.test(path)) return route.fulfill({status:404,json:{code:404,msg:'usage unavailable'}});
    throw new Error('Unexpected API '+path);
  }
 });
 const page=await context.newPage(); page.setDefaultTimeout(2000); page.setDefaultNavigationTimeout(10000); page.on('pageerror',e=>errors.push(e.message)); await page.goto(origin); await page.getByText('Conversation a',{exact:true}).waitFor();
 const select=id=>page.getByText(`Conversation ${id}`,{exact:true}).first().click();
 return {page,select,requests,errors,check:()=>assert.deepEqual(errors,[])};
}

test('plans remain in their own user turns',async t=>{
 const a=await app(t,{history:[msg('u1','chat','first request'),msg('p2','plan','',{payload:{steps:[{title:'First task plan',status:'done'}]}}),msg('u3','chat','second request'),msg('p4','plan','',{meta:{conversation_id:'a',run_id:'run-b'},payload:{steps:[{title:'Second task plan',status:'active'}]}})]});
 await a.select('a'); await a.page.getByText('First task plan',{exact:true}).waitFor(); await a.page.getByText('Second task plan',{exact:true}).waitFor(); a.check();
});
test('draft text and uploaded attachments survive conversation switches',async t=>{
 const a=await app(t); await a.select('a'); await a.page.locator('textarea').fill('draft for A');
 await a.page.locator('input[type=file]').setInputFiles({name:'input.txt',mimeType:'text/plain',buffer:Buffer.from('fixture')});
 await a.page.getByRole('button',{name:'移除 input.txt'}).waitFor(); await a.select('b'); assert.equal(await a.page.locator('textarea').inputValue(),'');
 await a.page.locator('textarea').fill('draft for B'); await a.select('a'); assert.equal(await a.page.locator('textarea').inputValue(),'draft for A'); await a.page.getByRole('button',{name:'移除 input.txt'}).waitFor(); a.check();
});
test('rejected send preserves draft and attachment',async t=>{
 const a=await app(t); await a.select('a'); await a.page.locator('textarea').fill('retain this input');
 await a.page.locator('input[type=file]').setInputFiles({name:'input.txt',mimeType:'text/plain',buffer:Buffer.from('fixture')}); await a.page.getByRole('button',{name:'移除 input.txt'}).waitFor();
 await a.page.locator('textarea').press('Enter'); await a.page.getByText('发送失败',{exact:false}).first().waitFor(); assert.equal(await a.page.locator('textarea').inputValue(),'retain this input'); await a.page.getByRole('button',{name:'移除 input.txt'}).waitFor(); a.check();
});
test('configuration read failure cannot save defaults and can retry',async t=>{
 const a=await app(t,{configFails:true}); await a.page.getByRole('button',{name:'模型配置',exact:true}).click(); await a.page.getByRole('alert').filter({hasText:'configuration unavailable'}).waitFor();
 assert.equal(await a.page.getByRole('button',{name:'保存配置',exact:true}).isEnabled().catch(()=>false),false); await a.page.getByRole('button',{name:'重新读取配置'}).waitFor(); a.check();
});
test('empty model catalog after saving keeps a working picker',async t=>{
 const a=await app(t,{emptyModels:true}); await a.page.getByRole('button',{name:'模型配置',exact:true}).click(); await a.page.getByRole('button',{name:'保存配置',exact:true}).click(); await a.page.getByText('配置已保存，对下一次发送生效。').waitFor(); await a.page.getByRole('button',{name:'关闭模型配置'}).click(); await a.page.getByRole('button',{name:'选择模型'}).waitFor(); a.check();
});
test('followups generate automatically after completion and cache across conversation switches',async t=>{
 const a=await app(t,{history:[msg('u1','chat','question'),msg('a2','chat','answer',{role:'assistant',meta:{conversation_id:'a',run_id:'run-a',model_version:'model-a',model_profile:'profile-1'}}),msg('d3','task','',{action:'done'})]});
 await a.select('a'); await a.page.getByText('answer',{exact:true}).waitFor(); assert.equal(await a.page.getByRole('button',{name:'生成追问建议'}).count(),0);
 await a.page.getByRole('button',{name:'Follow up once',exact:false}).waitFor(); await a.select('b'); await a.select('a'); await a.page.getByRole('button',{name:'Follow up once',exact:false}).waitFor(); assert.equal(a.requests.filter(r=>r.path==='/chat/followups').length,1); a.check();
});
test('file reference picker navigates into directories',async t=>{
 const a=await app(t); await a.select('a'); await a.page.locator('textarea').fill('@'); await a.page.locator('textarea').press('ArrowRight'); await a.page.getByRole('button',{name:'打开目录 reports'}).click(); await a.page.getByRole('button',{name:'引用 reports/deep.txt'}).click(); await a.page.getByRole('button',{name:'移除 reports/deep.txt'}).waitFor(); a.check();
});
test('history resume rejects unconfirmed response but allows legacy exhausted runs',async t=>{
 const a=await app(t,{runs:[record(),record({run_id:'exhausted',budget_hit:'tokens',conversation_id:'b'})]}); await a.page.getByRole('button',{name:'切换工作台面板'}).click(); await a.page.getByRole('tab',{name:'运营台',exact:false}).first().click();
 await a.page.getByRole('button',{name:'继续任务',exact:true}).first().click(); await a.page.getByText('服务未确认该任务继续运行',{exact:false}).first().waitFor(); assert.equal(a.requests.filter(r=>r.path==='/chat/resume_run').length,1); assert.equal(await a.page.getByRole('button',{name:'继续任务',exact:true}).count(),2); a.check();
});
test('stop failure is visible and does not claim idle',async t=>{
 const a=await app(t,{handle:async({path,route})=>{if(path==='/chat/run'){await route.fulfill({contentType:'text/event-stream',body:''});return true;} }}); await a.select('a'); await a.page.locator('textarea').fill('long task'); await a.page.locator('textarea').press('Enter'); await a.page.getByRole('button',{name:'停止',exact:true}).click(); await a.page.getByText('停止失败',{exact:false}).first().waitFor(); await a.page.getByRole('button',{name:'重新连接',exact:true}).waitFor(); a.check();
});

test('named profiles isolate discovery and explicitly probe saved capabilities',async t=>{
 let data={active_profile_id:'one',profiles:[{id:'one',name:'First account',protocol:'openai-compatible',provider:'custom',base_url:'https://example.invalid/v1',models:['model-a'],enabled:true,api_key_set:true,verified:{}}]};
 const a=await app(t,{handle:async({path,body,json,requests})=>{
  if(path==='/model-profiles/get'){await json(data);return true;}
  if(path==='/model-profiles/save'){assert.ok(body.profiles.every(p=>!('verified' in p)));data={...body,profiles:body.profiles.map(p=>({...p,api_key_set:true,api_key:undefined,verified:{}}))};await json(data);return true;}
  if(path==='/model-settings/discover'){assert.equal(body.profile_id,'one');assert.equal(body.protocol,'openai-compatible');await json({models:['model-a','model-b'],source:'remote'});return true;}
  if(path==='/model-profiles/probe'){assert.equal(body.profile_id,'one');assert.deepEqual(body.capabilities,['text']);await json({text:{verified:true,checked_at:'2026-09-15T10:00:00.123Z'}});return true;}
 }});
 await a.page.getByRole('button',{name:'模型配置',exact:true}).click();await a.page.getByRole('button',{name:'命名连接与能力检测'}).click();await a.page.getByLabel('连接名称').fill('Renamed account');await a.page.getByRole('button',{name:'发现此连接模型'}).click();await a.page.getByRole('button',{name:'保存所有连接'}).click();await a.page.getByText('连接配置已保存',{exact:true}).waitFor();
 assert.equal(a.requests.filter(r=>r.path==='/model-profiles/probe').length,0);
 await a.page.getByRole('button',{name:'检测 文本 model-a'}).click();await a.page.getByText('文本：已验证',{exact:false}).first().waitFor();assert.equal(a.requests.filter(r=>r.path==='/model-profiles/probe').length,1);a.check();
});

test('a stream gap reconciles persisted history once and resumes without duplicates',async t=>{
 let gaps=0;
 const history=[msg('u1','chat','recover gap'),msg('a2','chat','Persisted answer',{role:'assistant'})];
 const a=await app(t,{handle:async({path,body,json,route,url})=>{
  if(path==='/conversation/get-messages'){await json(gaps?history:[]);return true;}
  if(path==='/chat/run'){await route.fulfill({contentType:'text/event-stream',body:`id: 1\ndata: ${JSON.stringify(msg('a2','chat','Persisted answer',{role:'assistant',meta:{conversation_id:body.conversation_id,run_id:'run-a'}}))}\n\n`});return true;}
  if(path==='/chat/attach'){
   if(!url.searchParams.has('reconcile')){assert.equal(url.searchParams.get('run_id'),'run-a');gaps++;await route.fulfill({status:409,json:{code:409,msg:'stream history gap',data:{reconcile:true,run_id:'run-a'}}});return true;}
   assert.equal(url.searchParams.get('reconcile'),'1');assert.equal(url.searchParams.get('last_event_id'),'0');
   const events=[msg('s3','stream','',{action:'snapshot',payload:{run_id:'run-a',cursor:3}}),msg('d4','task','',{action:'done'})];await route.fulfill({contentType:'text/event-stream',body:events.map((m,i)=>`id: ${i+3}\ndata: ${JSON.stringify(m)}\n\n`).join('')});return true;
  }
 }});await a.select('a');await a.page.locator('textarea').fill('recover gap');await a.page.locator('textarea').press('Enter');await a.page.waitForResponse(r=>r.url().includes('reconcile=1'));assert.equal(gaps,1);assert.equal(await a.page.getByText('Persisted answer',{exact:true}).count(),1);a.check();
});

test('retry retains original attachment, model and confirmation settings',async t=>{
 const a=await app(t,{handle:async({path,body,route})=>{
  if(path==='/chat/run'){const meta={conversation_id:body.conversation_id,run_id:'run-a'};await route.fulfill({contentType:'text/event-stream',body:[msg('a2','chat','Partial output',{role:'assistant',meta}),msg('d3','task','',{action:'partial',meta})].map(m=>`data: ${JSON.stringify(m)}\n\n`).join('')});return true;}
 }});await a.select('a');await a.page.getByRole('button',{name:'选择模型'}).click();await a.page.getByRole('menuitem').filter({hasText:'model-a'}).click();await a.page.locator('textarea').fill('original with file');await a.page.locator('input[type=file]').setInputFiles({name:'source.txt',mimeType:'text/plain',buffer:Buffer.from('data')});await a.page.getByRole('button',{name:'移除 source.txt'}).waitFor();await a.page.locator('textarea').press('Enter');await a.page.getByText('Partial output',{exact:true}).waitFor();
 await a.page.getByRole('button',{name:'选择模型'}).click();await a.page.getByRole('menuitem').filter({hasText:'Auto'}).click();await a.page.getByRole('button',{name:'重新生成',exact:true}).click();
 await a.page.waitForRequest(r=>new URL(r.url()).pathname.endsWith('/run/list')).catch(()=>{});
 const sends=a.requests.filter(r=>r.path==='/chat/run');assert.equal(sends.length,2);assert.deepEqual(sends[1].body,sends[0].body);a.check();
});
test('new-conversation rejected send retains input under the created conversation',async t=>{
 const a=await app(t);await a.page.locator('textarea').fill('new failed draft');await a.page.locator('textarea').press('Enter');await a.page.getByText('发送失败',{exact:false}).first().waitFor();assert.equal(await a.page.locator('textarea').inputValue(),'new failed draft');a.check();
});
test('history pagination advances offset and attention filter requests unfinished states',async t=>{
 const a=await app(t,{handle:async({path,body,json})=>{
  if(path==='/run/list'){await json({runs:[record({run_id:`page-${body.offset||0}`,prompt:`Page offset ${body.offset||0}`})],has_more:!body.offset});return true;}
 }});await a.page.getByRole('button',{name:'切换工作台面板'}).click();await a.page.getByRole('tab',{name:'运营台'}).click();await a.page.getByText('Page offset 0',{exact:true}).waitFor();await a.page.getByRole('button',{name:'下一页'}).click();await a.page.getByText('Page offset 50',{exact:true}).waitFor();await a.page.getByRole('button',{name:'需要处理',exact:true}).click();await a.page.getByText('Page offset 0',{exact:true}).waitFor();assert.ok(a.requests.some(r=>r.path==='/run/list'&&r.body.statuses?.includes('partial')&&r.body.statuses?.includes('interrupted')));a.check();
});

test('retry pins the answered model profile and shows a stale-profile rejection',async t=>{
 let count=0;
 const a=await app(t,{handle:async({path,body,route})=>{
  if(path!=='/chat/run')return false;
  if(count++){assert.equal(body.model_profile,'profile-original');await route.fulfill({status:409,json:{code:409,msg:'模型配置已变化，请重新选择模型'}});return true;}
  const meta={conversation_id:'a',run_id:'run-a',model_version:'model-a',model_profile:'profile-original'};await route.fulfill({contentType:'text/event-stream',body:[msg('a2','chat','Frozen model answer',{role:'assistant',meta}),msg('d3','task','',{action:'done',meta})].map(m=>`data: ${JSON.stringify(m)}\n\n`).join('')});return true;
 }});await a.select('a');await a.page.locator('textarea').fill('original request');await a.page.locator('textarea').press('Enter');await a.page.getByRole('button',{name:'重新生成',exact:true}).click();await a.page.getByText('模型配置已变化，请重新选择模型',{exact:false}).first().waitFor();assert.equal(count,2);a.check();
});

test('automatic tools require no selection and allow an optional restricted range',async t=>{
 const a=await app(t,{handle:async({path,body,json,route})=>{
  if(path==='/tools/catalog'){await json([{name:'python',group:'code',description:'Run Python',danger:true},{name:'file_read',group:'file',description:'Read file',danger:false}]);return true;}
  if(path==='/chat/run'){await route.fulfill({contentType:'text/event-stream',body:[msg('a2','chat','Tool result',{role:'assistant',meta:{conversation_id:body.conversation_id}}),msg('d3','task','',{action:'done',meta:{conversation_id:body.conversation_id}})].map(m=>`data: ${JSON.stringify(m)}\n\n`).join('')});return true;}
 }});await a.page.getByRole('button',{name:'工具范围'}).click();await a.page.getByText('自动使用全部可用工具',{exact:false}).waitFor();assert.equal(a.requests.filter(r=>r.path==='/conversation/create-conversation').length,0);
 await a.select('a');await a.page.locator('textarea').fill('use available tools automatically');await a.page.locator('textarea').press('Enter');await a.page.getByText('Tool result',{exact:true}).waitFor();assert.deepEqual(a.requests.find(r=>r.path==='/chat/run').body.enabled_tools,[]);
 await a.page.getByRole('button',{name:'代码',exact:true}).click();await a.page.locator('textarea').fill('restrict this run to code');await a.page.locator('textarea').press('Enter');await a.page.getByRole('button',{name:'重新生成',exact:true}).waitFor();assert.ok(a.requests.filter(r=>r.path==='/chat/run').at(-1).body.enabled_tools.includes('code'));assert.equal(await a.page.getByRole('dialog').count(),0);a.check();
});

test('stop waits for confirmation and settles without reattaching a stopped task',async t=>{
 let release;const gate=new Promise(r=>release=r);
 const a=await app(t,{handle:async({path,route,json})=>{
  if(path==='/chat/run'){await route.fulfill({contentType:'text/event-stream',body:''});return true;}
  if(path==='/chat/kill'){await gate;await json({status:'killed'});return true;}
 }});await a.select('a');await a.page.locator('textarea').fill('stop after confirmation');await a.page.locator('textarea').press('Enter');await a.page.getByRole('button',{name:'停止',exact:true}).click();await a.page.getByText('正在停止，等待服务确认…',{exact:true}).waitFor();
 await a.page.waitForTimeout(750);await a.page.getByText('正在停止，等待服务确认…',{exact:true}).waitFor();release();await a.page.getByText('正在停止，等待服务确认…',{exact:true}).waitFor({state:'hidden'});await a.page.getByRole('button',{name:'停止',exact:true}).waitFor({state:'hidden'});a.check();
});
test('produced file event refreshes the already open files panel',async t=>{
 let generated=false;
 const a=await app(t,{handle:async({path,json,route})=>{
  if(path==='/file/list'){await json(generated?[{name:'fresh.txt',dir:false,size:4}]:[]);return true;}
  if(path==='/chat/run'){generated=true;await route.fulfill({contentType:'text/event-stream',body:[msg('t2','tool','',{payload:{tool:'file_write',args:{path:'fresh.txt'},result:'written'}}),msg('d3','task','',{action:'done'})].map(m=>`data: ${JSON.stringify(m)}\n\n`).join('')});return true;}
 }});await a.select('a');await a.page.getByRole('button',{name:'切换工作台面板'}).click();await a.page.getByRole('tab',{name:'成果'}).click();await a.page.locator('textarea').fill('generate file');await a.page.locator('textarea').press('Enter');await a.page.getByRole('button',{name:'fresh.txt',exact:true}).waitFor();a.check();
});
test('repeated reconciliation gap exits and offers manual reconnect',async t=>{
 let gaps=0;
 const a=await app(t,{handle:async({path,route})=>{
  if(path==='/chat/run'){await route.fulfill({contentType:'text/event-stream',body:''});return true;}
  if(path==='/chat/attach'){gaps++;await route.fulfill({status:409,json:{code:409,msg:'stream history gap',data:{reconcile:true,run_id:'run-a'}}});return true;}
 }});await a.select('a');await a.page.locator('textarea').fill('gap twice');await a.page.locator('textarea').press('Enter');await a.page.getByRole('button',{name:'重新连接',exact:true}).waitFor();assert.equal(gaps,2);a.check();
});

test('advanced system status shows real readiness without claiming a model probe',async t=>{
 const a=await app(t,{handle:async({path,json,route})=>{if(path==='/system/status'){assert.equal(route.request().method(),'GET');await json({version:'test-revision',build_time:'2026-09-15T10:00:00Z',modified:true,started_at:'2026-09-15T10:01:00Z',ready:false,services:[{name:'tools',status:'unavailable',detail:'fixture unavailable'}],model_probe:'not_run'});return true;}}});
 await a.page.getByRole('button',{name:'切换工作台面板'}).click();await a.page.getByRole('tab',{name:'运营台'}).click();await a.page.getByRole('button',{name:'专业功能 展开'}).click();await a.page.getByRole('button',{name:'服务状态',exact:true}).click();await a.page.getByText('test-revision',{exact:false}).waitFor();await a.page.getByText('fixture unavailable',{exact:false}).waitFor();await a.page.getByText('模型调用检测：未执行',{exact:true}).waitFor();a.check();
});

test('XLSX preview switches sheets and displays cached formula results without evaluating formulas',async t=>{
 const {utils,write}=await import('xlsx');const workbook=utils.book_new();const first=utils.aoa_to_sheet([['Input','Total'],[3,6]]);first.B2={t:'n',v:6,f:'A2*2'};utils.book_append_sheet(workbook,first,'Summary');utils.book_append_sheet(workbook,utils.aoa_to_sheet([['Name'],['Second sheet data']]),'Details');const bytes=write(workbook,{type:'buffer',bookType:'xlsx'});
 const a=await app(t,{handle:async({path,json,route})=>{
  if(path==='/file/list'){await json([{name:'report.xlsx',dir:false,size:bytes.length}]);return true;}
  if(path==='/file/download'){await route.fulfill({contentType:'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',body:bytes});return true;}
 }});await a.select('a');await a.page.getByRole('button',{name:'切换工作台面板'}).click();await a.page.getByRole('tab',{name:'成果'}).click();await a.page.getByRole('button',{name:'report.xlsx',exact:true}).click();await a.page.getByRole('cell',{name:'6',exact:true}).waitFor();await a.page.getByText('公式显示文件中保存的缓存结果，不重新计算。',{exact:true}).waitFor();await a.page.getByRole('tab',{name:'Details',exact:true}).click();await a.page.getByRole('cell',{name:'Second sheet data',exact:true}).waitFor();a.check();
});


test('acceptance evidence stays in workbench history and distinguishes unchecked and unverified runs',async t=>{
 let checked=false;
 const a=await app(t,{history:[msg('u1','chat','Verify this')],runs:[record()],handle:async({path,body,json})=>{
  if(path==='/run/acceptance'){assert.equal(body.run_id,'run-a');await json({contract:{run_id:'run-a',requests:['Produce checked report'],truncated:false},checks:checked?[{at:'2026-09-15T10:00:00Z',spec_path:'acceptance.json',report:{ok:false,scope:'delivery',results:[{id:'chart',description:'Chart evidence',method:'manual',status:'unverified',detail:'No browser evidence'}]}}]:[]});return true;}
 }});await a.select('a');assert.equal(await a.page.getByRole('region',{name:'当前任务摘要'}).count(),0);assert.equal(await a.page.getByRole('main').getByRole('button',{name:'验收证据'}).count(),0);await openWorkbench(a,'运营台');await a.page.getByRole('article').getByRole('button',{name:'验收证据'}).click();await a.page.getByText('尚未检查，不能判定验收通过。',{exact:true}).waitFor();assert.equal(await a.page.getByText('检查通过',{exact:true}).count(),0);
 checked=true;await a.page.getByRole('button',{name:'刷新验收记录'}).click();await a.page.getByText('No browser evidence',{exact:true}).waitFor();await a.page.getByText('未验证 · Chart evidence',{exact:true}).waitFor();a.check();
});

test('delivery snapshots stay separate from current workspace and download with conversation authentication',async t=>{
 const a=await app(t,{handle:async({path,body,json,route,url})=>{
  if(path==='/delivery/list'){assert.equal(body.conversation_id,'a');await json({deliveries:[{version:1,conversation_id:'a',run_id:'run-a',created_at:'2026-09-15T10:00:00Z',files:[{path:'reports/final.txt',size:6,sha256:'fixture-checksum'}]}]});return true;}
  if(path==='/delivery/download'){assert.equal(url.searchParams.get('conversation_id'),'a');assert.equal(url.searchParams.get('run_id'),'run-a');assert.equal(url.searchParams.get('path'),'reports/final.txt');assert.equal(url.searchParams.get('token'),'isolated-hardening-test');await route.fulfill({headers:{'content-disposition':'attachment; filename="final.txt"'},body:'frozen'});return true;}
 }});await a.select('a');await a.page.getByRole('button',{name:'切换工作台面板'}).click();await a.page.getByRole('tab',{name:'成果'}).click();await a.page.getByText('当前工作区',{exact:true}).waitFor();await a.page.getByRole('button',{name:'交付快照'}).click();await a.page.getByText('fixture-checksum',{exact:false}).waitFor();const download=a.page.waitForEvent('download');await a.page.getByRole('link',{name:'下载快照 reports/final.txt'}).click();assert.equal((await download).suggestedFilename(),'final.txt');a.check();
});

test('system status HTTP errors are visible and can retry',async t=>{
 const a=await app(t,{handle:async({path,route})=>{if(path==='/system/status'){await route.fulfill({status:403,json:{msg:'status access denied'}});return true;}}});await a.page.getByRole('button',{name:'切换工作台面板'}).click();await a.page.getByRole('tab',{name:'运营台'}).click();await a.page.getByRole('button',{name:'专业功能 展开'}).click();await a.page.getByRole('button',{name:'服务状态',exact:true}).click();await a.page.getByRole('alert').filter({hasText:'status access denied'}).waitFor();assert.equal(await a.page.getByText('服务已就绪',{exact:true}).count(),0);a.check();
});

test('historical file links bind only the message run and matching manifest path',async t=>{
 const a=await app(t,{history:[
  msg('a1','chat','[Historical report](reports/final.txt)',{role:'assistant',meta:{conversation_id:'a',run_id:'old-run'}}),
  msg('a2','chat','[Unpublished report](reports/final.txt)',{role:'assistant',meta:{conversation_id:'a',run_id:'new-run'}}),
  msg('a3','chat','[Legacy report](reports/final.txt)',{role:'assistant',meta:{conversation_id:'a'}}),
  msg('a4','chat','[Unlisted report](reports/other.txt)',{role:'assistant',meta:{conversation_id:'a',run_id:'old-run'}})
 ],handle:async({path,json})=>{if(path==='/delivery/list'){await json({deliveries:[{version:1,conversation_id:'a',run_id:'old-run',created_at:1,files:[{path:'reports/final.txt',size:2,sha256:'test'}]},{version:2,conversation_id:'b',run_id:'new-run',created_at:2,files:[{path:'reports/final.txt',size:3,sha256:'foreign'}]}]});return true;}}});
 await a.select('a');const historical=a.page.getByRole('link',{name:'Historical report',exact:false});await a.page.getByText('交付快照 · 版本 1',{exact:true}).waitFor();const pinned=new URL(await historical.getAttribute('href'),origin);assert.equal(pinned.pathname,BASE+'/delivery/download');assert.equal(pinned.searchParams.get('run_id'),'old-run');
 for(const name of ['Unpublished report','Legacy report','Unlisted report']){const link=a.page.getByRole('link',{name,exact:false});assert.ok((await link.textContent()).includes('当前工作区'));assert.equal(new URL(await link.getAttribute('href'),origin).pathname,BASE+'/file/download');}a.check();
});

test('live browser evidence never embeds a shared VNC desktop',async t=>{
 const frame='iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=';
 const a=await app(t,{handle:async({path,route})=>{if(path==='/chat/run'){await route.fulfill({contentType:'text/event-stream',body:`data: ${JSON.stringify(msg('s1','browser','',{payload:{data:frame,url:'https://example.invalid',action:'screenshot'}}))}\n\n`});return true;}}});await a.select('a');await a.page.locator('textarea').fill('browser task');await a.page.locator('textarea').press('Enter');await a.page.getByAltText('本次运行截图').waitFor();assert.equal(await a.page.locator('iframe').count(),0);assert.equal(await a.page.locator('a[href*="6080"]').count(),0);assert.equal(await a.page.getByAltText('本次运行截图').getAttribute('src'),'data:image/png;base64,'+frame);a.check();
});

test('usage evidence exposes unknown usage and retries never send limits',async t=>{
 const a=await app(t,{runs:[record()],handle:async({path,body,json,route})=>{
  if(path==='/run/run-a/budget'){assert.equal(route.request().method(),'GET');await json({run_id:'run-a',budget_run_id:'workflow-parent',status:'partial',limits:{max_tokens:2000000,max_wall_seconds:7200,max_steps:300},used_tokens:110,reserved_tokens:20,remaining_tokens:1999870,unknown_calls:1,unknown_tokens:100,estimated_tokens:30,used_steps:2,deadline:'2026-09-15T18:00:00Z',sources:[{source:'gui',used_tokens:110,reserved_tokens:20,unknown_calls:1,unknown_tokens:100,estimated_tokens:30}]});return true;}
  if(path==='/chat/run'){await route.fulfill({contentType:'text/event-stream',body:[msg('a2','chat','Budget output',{role:'assistant'}),msg('d3','task','',{action:'partial'})].map(m=>`data: ${JSON.stringify(m)}\n\n`).join('')});return true;}
 }});await a.select('a');await openBudget(a);await a.page.locator('textarea').fill('budgeted task');await a.page.locator('textarea').press('Enter');await a.page.getByText('Budget output',{exact:true}).waitFor();await openMetrics(a);await a.page.getByText('累计用量 110 tokens（含未知调用的保守占用） · 在途 20',{exact:true}).waitFor();await a.page.getByRole('button',{name:'用量明细'}).click();await a.page.getByText('未知用量：1 次调用，保守占用 100 tokens',{exact:true}).waitFor();await a.page.getByText('共享运行：workflow-parent',{exact:true}).waitFor();await openBudget(a);await a.page.getByRole('button',{name:'重新生成',exact:true}).click();await a.page.waitForRequest(r=>new URL(r.url()).pathname.endsWith('/run/list')).catch(()=>{});const sends=a.requests.filter(r=>r.path==='/chat/run');assert.equal(sends.length,2);assert.equal(sends[0].body.budget,undefined);assert.deepEqual(sends[1].body.budget,sends[0].body.budget);a.check();
});

test('complete product path from named connection and capability to metered execution, private GUI, delivery and acceptance',async t=>{
 let submitted=false;
 const profile={id:'chain',name:'Verified account',protocol:'openai-compatible',provider:'custom',base_url:'https://example.invalid/v1',models:['model-a'],enabled:true,api_key_set:true,verified:{}};
 const frame='iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=';
 const a=await app(t,{handle:async({path,body,json,route})=>{
  if(path==='/model-profiles/get'){await json({active_profile_id:'chain',profiles:[profile]});return true;}
  if(path==='/model-profiles/save'){assert.equal(body.active_profile_id,'chain');assert.ok(!('verified' in body.profiles[0]));await json({active_profile_id:'chain',profiles:[profile]});return true;}
  if(path==='/model-profiles/probe'){assert.equal(body.profile_id,'chain');assert.deepEqual(body.capabilities,['tools']);await json({tools:{verified:true,checked_at:'2026-09-15T10:00:00Z'}});return true;}
  if(path==='/chat/run'){
   assert.equal(body.budget,undefined);submitted=true;
   const meta={conversation_id:'a',run_id:'run-a',model_version:'model-a',model_profile:'chain-revision'};
   await route.fulfill({contentType:'text/event-stream',body:[msg('b2','browser','',{meta,payload:{data:frame,action:'screenshot'}}),msg('a3','chat','[Final report](final.txt)',{role:'assistant',meta}),msg('d4','task','',{action:'done',meta})].map(m=>`data: ${JSON.stringify(m)}\n\n`).join('')});return true;
  }
  if(path==='/run/list'){await json({runs:submitted?[record({status:'done',resumable:false})]:[]});return true;}
  if(path==='/delivery/list'){await json({deliveries:submitted?[{version:1,conversation_id:'a',run_id:'run-a',created_at:1,files:[{path:'final.txt',size:6,sha256:'frozen-checksum'}]}]:[]});return true;}
  if(path==='/run/acceptance'){await json({contract:{run_id:'run-a',requests:['Produce final report'],truncated:false},checks:[{at:'2026-09-15T10:00:00Z',spec_path:'acceptance.json',report:{ok:true,scope:'delivery',results:[{id:'file',description:'File exists',method:'sha256',file:'final.txt',status:'passed',sha256:'frozen-checksum'}]}}]});return true;}
  if(path==='/delivery/download'){await route.fulfill({headers:{'content-disposition':'attachment; filename="final.txt"'},body:'frozen'});return true;}
 }});
 await a.page.getByRole('button',{name:'模型配置',exact:true}).click();await a.page.getByRole('button',{name:'命名连接与能力检测'}).click();await a.page.getByRole('button',{name:'保存所有连接'}).click();await a.page.getByText('连接配置已保存',{exact:true}).waitFor();await a.page.getByRole('button',{name:'检测 工具调用 model-a'}).click();await a.page.getByText('工具调用：已验证',{exact:false}).waitFor();await a.page.getByRole('button',{name:'关闭命名连接'}).click();await a.page.getByRole('button',{name:'关闭模型配置'}).click();
 await a.select('a');await openBudget(a);await a.page.locator('textarea').fill('Complete delivery');await a.page.locator('textarea').press('Enter');await a.page.getByRole('button',{name:'停止',exact:true}).waitFor({state:'hidden'});await a.page.getByText('交付快照 · 版本 1',{exact:true}).waitFor();await a.page.getByRole('button',{name:/查看 · 1 步/}).click();await a.page.getByAltText('本次运行截图').waitFor();assert.equal(await a.page.locator('iframe').count(),0);
 await openWorkbench(a,'运营台');await a.page.getByRole('button',{name:'验收证据',exact:true}).click();await a.page.getByText('检查通过',{exact:true}).waitFor();await openWorkbench(a,'成果');await a.page.getByRole('button',{name:'交付快照',exact:true}).click();await a.page.getByText('当前工作区',{exact:true}).waitFor();const download=a.page.waitForEvent('download');await a.page.getByRole('link',{name:'下载快照 final.txt'}).click();assert.equal((await download).suggestedFilename(),'final.txt');a.check();
});

test('session drafts reload with attachments and conversation-specific model and scope',async t=>{
 const a=await app(t,{handle:async({path,json})=>{if(path==='/tools/catalog'){await json([{name:'python',group:'code',description:'Python',danger:true}]);return true;}}});
 await a.select('a');await a.page.getByRole('button',{name:'选择模型'}).click();await a.page.getByRole('menuitem').filter({hasText:'model-a'}).click();await a.page.getByRole('button',{name:'工具范围'}).click();await a.page.getByRole('button',{name:'代码',exact:true}).click();await a.page.getByRole('button',{name:'工具范围'}).click();await a.page.locator('textarea').fill('persistent draft A');await a.page.locator('input[type=file]').setInputFiles({name:'reload.txt',mimeType:'text/plain',buffer:Buffer.from('fixture')});await a.page.getByRole('button',{name:'移除 reload.txt'}).waitFor();await openBudget(a);await a.select('b');await a.page.locator('textarea').fill('persistent draft B');
 await a.page.reload();await a.select('a');assert.equal(await a.page.locator('textarea').inputValue(),'persistent draft A');await a.page.getByRole('button',{name:'移除 reload.txt'}).waitFor();assert.ok((await a.page.getByRole('button',{name:'选择模型'}).textContent()).includes('model-a'));await openBudget(a);await a.page.locator('textarea').press('Enter');await a.page.getByText('发送失败',{exact:false}).first().waitFor();const sent=a.requests.filter(r=>r.path==='/chat/run').at(-1).body;assert.deepEqual(sent.file_ids,['reload.txt']);assert.deepEqual(sent.enabled_tools,['code']);assert.equal(sent.selected_version,'model-a');assert.equal(sent.budget,undefined);
 await a.page.reload();await a.select('a');assert.equal(await a.page.locator('textarea').inputValue(),'persistent draft A');await a.page.getByRole('button',{name:'移除 reload.txt'}).waitFor();await a.select('b');assert.equal(await a.page.locator('textarea').inputValue(),'persistent draft B');a.check();
});

test('complete retry survives reload and remains separate from the newly edited draft',async t=>{
 let history=[];
 const a=await app(t,{handle:async({path,body,json,route})=>{
  if(path==='/conversation/get-messages'){await json(history);return true;}
  if(path==='/tools/catalog'){await json([{name:'python',group:'code',description:'Python',danger:true}]);return true;}
  if(path==='/chat/run'){history=[msg('u1','chat',body.message),msg('a2','chat','Retry across reload',{role:'assistant',meta:{conversation_id:'a',run_id:'run-a',model_version:'model-a',model_profile:'immutable-revision'}}),msg('d3','task','',{action:'partial'})];await route.fulfill({contentType:'text/event-stream',body:history.slice(1).map(m=>`data: ${JSON.stringify(m)}\n\n`).join('')});return true;}
 }});await a.select('a');await a.page.getByRole('button',{name:'选择模型'}).click();await a.page.getByRole('menuitem').filter({hasText:'model-a'}).click();await a.page.getByRole('button',{name:'工具范围'}).click();await a.page.getByRole('button',{name:'代码',exact:true}).click();await a.page.getByRole('button',{name:'工具范围'}).click();await a.page.locator('textarea').fill('Original exact input');await a.page.locator('input[type=file]').setInputFiles({name:'original.txt',mimeType:'text/plain',buffer:Buffer.from('fixture')});await a.page.getByRole('button',{name:'移除 original.txt'}).waitFor();await openBudget(a);await a.page.locator('textarea').press('Enter');await a.page.getByText('Retry across reload',{exact:true}).waitFor();await a.page.locator('textarea').fill('Next unsent draft');await a.page.getByRole('button',{name:'选择模型'}).click();await a.page.getByRole('menuitem').filter({hasText:'Auto'}).click();
 await a.page.reload();await a.select('a');await a.page.getByRole('button',{name:'重新生成',exact:true}).click();await a.page.getByText('Retry across reload',{exact:true}).waitFor();assert.equal(await a.page.locator('textarea').inputValue(),'Next unsent draft');const sends=a.requests.filter(r=>r.path==='/chat/run');assert.equal(sends.length,2);assert.deepEqual(sends[1].body,{...sends[0].body,model_profile:'immutable-revision'});const persisted=await a.page.evaluate(()=>Object.keys(sessionStorage).filter(k=>k.startsWith('orka.session.')).map(k=>[k,sessionStorage.getItem(k)]));assert.ok(persisted.length>0);assert.ok(!JSON.stringify(persisted).includes('isolated-hardening-test'));a.check();
});

test('logout clears persisted drafts and retries before a different owner signs in',async t=>{
 let owner='test@example.invalid';const a=await app(t,{handle:async({path,json})=>{if(path==='/auth/me'){await json({email:owner,name:'Test'});return true;}if(path==='/conversation/list'){await json(['a','b'].map(id=>({conversation_id:id,title:`Conversation ${id}`,owner_email:owner,created_at:1})));return true;}}});await a.select('a');await a.page.locator('textarea').fill('Owner A private draft');await a.page.locator('textarea').press('Enter');await a.page.getByText('发送失败',{exact:false}).first().waitFor();const count=()=>a.page.evaluate(()=>Object.keys(sessionStorage).filter(k=>k.startsWith('orka.session.')).length);assert.ok(await count()>0);await a.page.getByTitle('Sign out',{exact:true}).click();assert.equal(await count(),0);owner='other@example.invalid';await a.page.reload();await a.select('a');assert.equal(await a.page.locator('textarea').inputValue(),'');assert.equal(await a.page.getByRole('button',{name:'重新生成',exact:true}).count(),0);a.check();
});

test('expired session drafts and retry snapshots are not restored by reload',async t=>{
 const a=await app(t,{history:[msg('a1','chat','Previous answer',{role:'assistant'})]});await a.select('a');await a.page.locator('textarea').fill('expiring input');await a.page.locator('textarea').press('Enter');await a.page.getByText('发送失败',{exact:false}).first().waitFor();
 await a.page.evaluate(()=>{for(const key of Object.keys(sessionStorage)){if(!key.startsWith('orka.session.')||key==='orka.session.owner')continue;const entry=JSON.parse(sessionStorage.getItem(key));entry.expiresAt=Date.now()-1;sessionStorage.setItem(key,JSON.stringify(entry));}});
 await a.page.reload();await a.select('a');assert.equal(await a.page.locator('textarea').inputValue(),'');assert.equal(await a.page.getByRole('button',{name:'重新生成',exact:true}).count(),0);a.check();
});

test('overview retains usage but removes task limits, including historical snapshots',async t=>{
 const a=await app(t,{runs:[record({status:'done'})],history:[msg('a1','chat','Saved answer',{role:'assistant',meta:{conversation_id:'a',run_id:'run-a',model_version:'model-a'}})],handle:async({path,json})=>{
  if(path==='/run/run-a/budget'){await json({run_id:'run-a',budget_run_id:'run-a',status:'done',limits:{max_tokens:1234,max_steps:1},used_tokens:99,reserved_tokens:0,unknown_calls:0,unknown_tokens:0,estimated_tokens:0,used_steps:2,sources:[]});return true;}
 }});await a.select('a');await openMetrics(a);
 assert.equal(await a.page.getByText('任务预算',{exact:true}).count(),0);
 assert.equal(await a.page.getByLabel('Token 上限').count(),0);
 await a.page.getByRole('button',{name:'用量明细',exact:true}).click();
 await a.page.getByText('已用（含保守占用）99 · 预留 0 tokens',{exact:true}).waitFor();
 const details=a.page.getByRole('region',{name:'运行用量',exact:true});
 assert.ok(!(await details.textContent()).includes('上限'));assert.ok(!(await details.textContent()).includes('剩余'));
 a.check();
});


async function openWorkbench(a, tab) {
 const toggle=a.page.getByRole('button',{name:'切换工作台面板'});
 if(await toggle.getAttribute('aria-pressed')!=='true')await toggle.click();
 await a.page.getByRole('complementary',{name:'工作台'}).getByRole('tab',{name:tab,exact:false}).click();
}
// Existing workflows still visit the overview; it has no quota editor.
async function openBudget(a) { await openWorkbench(a,'概览'); }
async function openMetrics(a) {
 await openWorkbench(a,'概览');
 await a.page.getByText('累计指标',{exact:true}).waitFor();
}

test('pending conversation creation cannot retarget the captured send or its model and tools',async t=>{
 let release;const created=new Promise(resolve=>release=resolve);
 const a=await app(t,{handle:async({path,json,route})=>{
  if(path==='/conversation/create-conversation'){await created;await json({conversation_id:'new',title:'New',owner_email:'test@example.invalid'});return true;}
  if(path==='/tools/catalog'){await json([{name:'python',group:'code',description:'Python',danger:true}]);return true;}
  if(path==='/chat/run'){await route.fulfill({status:503,json:{code:503,msg:'fixture rejection'}});return true;}
 }});
 await a.page.getByRole('button',{name:'选择模型'}).click();await a.page.getByRole('menuitem').filter({hasText:'model-a'}).click();await a.page.getByRole('button',{name:'工具范围'}).click();await a.page.getByRole('button',{name:'代码',exact:true}).click();await a.page.getByRole('button',{name:'工具范围'}).click();await a.page.getByTitle(/^高危操作需确认/).click();await openBudget(a);await a.page.locator('textarea').fill('Send to the new conversation');const pending=a.page.waitForRequest(r=>new URL(r.url()).pathname.endsWith('/conversation/create-conversation'));await a.page.locator('textarea').press('Enter');await pending;
 await a.select('b');await a.page.locator('textarea').fill('Keep conversation B draft');const sent=a.page.waitForRequest(r=>new URL(r.url()).pathname.endsWith('/chat/run'));release();const body=(await sent).postDataJSON();assert.equal(body.conversation_id,'new');assert.equal(body.message,'Send to the new conversation');assert.equal(body.selected_version,'model-a');assert.deepEqual(body.enabled_tools,['code']);assert.equal(body.confirm_risky,false);assert.equal(body.budget,undefined);assert.equal(await a.page.locator('textarea').inputValue(),'Keep conversation B draft');a.check();
});

test('new actions match the compact neutral timeline capsules and keep explanations in tooltips',async t=>{
 const a=await app(t,{history:[msg('u1','chat','检查任务记录并整理交付结果'),...['t2','t3','t4'].map(id=>msg(id,'tool','',{payload:{tool:'web_search',args:{query:'fixture'},result:'资料已核对'}})),msg('a5','chat','任务记录已经整理，相关文件与验收证据可在工作台查看。',{role:'assistant',meta:{conversation_id:'a',run_id:'run-a',model_version:'model-a',model_profile:'profile-one'}}),msg('d6','task','',{action:'done'})]});await a.select('a');await a.page.addStyleTag({content:'*, *::before, *::after { transition: none !important; animation: none !important; }'});const follow=a.page.getByRole('button',{name:'Follow up once',exact:true});await follow.waitFor();assert.equal(await a.page.getByText('点击后使用本次回答的模型生成。',{exact:true}).count(),0);assert.equal(await a.page.getByRole('button',{name:'生成追问建议'}).count(),0);
 const style=await follow.evaluate(el=>{const s=getComputedStyle(el);return {radius:parseFloat(s.borderRadius),height:el.getBoundingClientRect().height,font:parseFloat(s.fontSize),border:s.borderColor,background:s.backgroundColor};});const timeline=await a.page.getByRole('button',{name:/查看 · 3 步/}).evaluate(el=>{const s=getComputedStyle(el);return {border:s.borderColor,background:s.backgroundColor};});assert.ok(style.radius>=14);assert.ok(style.height<=32);assert.ok(style.font<=13);assert.equal(style.border,timeline.border);assert.equal(style.background,timeline.background);assert.equal(await follow.locator('svg').count(),1);
 await a.page.getByTitle(/^高危操作需确认/).click();
 const headerStyle = async locator => locator.evaluate(el => { const s=getComputedStyle(el); return {height:el.getBoundingClientRect().height,radius:s.borderRadius,color:s.color,background:s.backgroundColor,font:s.fontSize}; });
 await a.page.mouse.move(0,0);
 const confirmation = await headerStyle(a.page.getByRole('button',{name:'不确认',exact:true}));
 for (const control of [a.page.getByRole('button',{name:'模型配置',exact:true}),a.page.getByRole('button',{name:'切换工作台面板'})]) assert.deepEqual(await headerStyle(control),confirmation);
 await a.page.getByRole('button',{name:'切换工作台面板'}).click(); assert.equal(await a.page.getByRole('button',{name:'切换工作台面板'}).getAttribute('aria-pressed'),'true'); assert.notEqual((await headerStyle(a.page.getByRole('button',{name:'切换工作台面板'}))).background,confirmation.background); await a.page.getByRole('button',{name:'切换工作台面板'}).click();
 if(process.env.ORKA_CAPTURE_SCREENSHOTS==='1'){const {mkdirSync}=await import('node:fs');mkdirSync(root+'tests/screenshots',{recursive:true});await a.page.screenshot({path:root+'tests/screenshots/chat-capsules.png'});await openBudget(a);await a.page.screenshot({path:root+'tests/screenshots/workbench-budget.png'});await openMetrics(a);await a.page.getByText('累计指标',{exact:true}).waitFor();await a.page.getByRole('region',{name:'当前运行统计'}).waitFor();await a.page.screenshot({path:root+'tests/screenshots/workbench-metrics.png'});}a.check();
});

test('workbench left separator resizes by pointer and keyboard, restores preference and clamps on viewport changes', async t => {
 const a=await app(t);await a.select('a');await openWorkbench(a,'概览');
 const panel=a.page.getByRole('complementary',{name:'工作台'}),handle=a.page.getByRole('separator',{name:'调整工作台宽度'});
 await handle.waitFor();const edge=await handle.boundingBox(),initial=(await panel.boundingBox()).width;
 await a.page.mouse.move(edge.x+edge.width/2,edge.y+100);await a.page.mouse.down();await a.page.mouse.move(edge.x+edge.width/2-160,edge.y+100,{steps:8});await a.page.mouse.up();assert.ok(Math.abs((await panel.boundingBox()).width-initial-160)<2);
 await handle.focus();await handle.press('End');assert.equal(await handle.getAttribute('aria-valuenow'),'720');await handle.press('Home');assert.equal(await handle.getAttribute('aria-valuenow'),'360');await handle.press('ArrowLeft');assert.equal(await handle.getAttribute('aria-valuenow'),'380');await handle.press('Shift+ArrowLeft');assert.equal(await handle.getAttribute('aria-valuenow'),'440');
 await a.page.reload();await a.select('a');await openWorkbench(a,'概览');assert.equal(await handle.getAttribute('aria-valuenow'),'440');await handle.press('End');
 await a.page.setViewportSize({width:693,height:1000});await handle.press('Home');assert.equal(await handle.getAttribute('aria-valuenow'),'360');const narrow=await handle.boundingBox();await a.page.mouse.move(narrow.x+narrow.width/2,narrow.y+100);await a.page.mouse.down();await a.page.mouse.move(narrow.x+narrow.width/2-160,narrow.y+100,{steps:8});await a.page.mouse.up();assert.equal(await handle.getAttribute('aria-valuenow'),'520');assert.ok((await panel.boundingBox()).width<=677);
 if(process.env.ORKA_CAPTURE_SCREENSHOTS==='1'){await a.page.screenshot({path:root+'tests/screenshots/workbench-693-drag.png'});}
 await a.page.setViewportSize({width:1440,height:1000});await a.page.waitForFunction(()=>document.querySelector('[role=separator]')?.getAttribute('aria-valuemax')==='720');await handle.press('End');
 await a.page.setViewportSize({width:800,height:1000});await a.page.waitForFunction(()=>document.querySelector('[role=separator]')?.getAttribute('aria-valuenow')==='480');assert.ok((await panel.boundingBox()).width<=480);
 await a.page.setViewportSize({width:340,height:850});await a.page.waitForFunction(()=>document.querySelector('aside[aria-label="工作台"]').getBoundingClientRect().width<=324);assert.ok((await panel.boundingBox()).width<=324);
 await a.page.setViewportSize({width:1440,height:1000});await a.page.waitForFunction(()=>document.querySelector('[role=separator]')?.getAttribute('aria-valuenow')==='720');
 await openWorkbench(a,'运营台');await a.page.getByRole('button',{name:'专业功能 展开',exact:true}).click();const nav=a.page.getByRole('button',{name:'服务状态',exact:true});await nav.scrollIntoViewIfNeeded();await nav.click();await a.page.getByRole('region',{name:'服务状态'}).waitFor();a.check();
});

test('automatic followups bind delayed responses to conversation and run and deduplicate revisits', async t => {
 let release;const gate=new Promise(resolve=>{release=resolve;});const aRun='run-a';
 const a=await app(t,{handle:async({path,body,json,route})=>{
  if(path==='/chat/run'){const meta={conversation_id:'a',run_id:'run-a-next',model_version:'model-a',model_profile:'profile-one'};await route.fulfill({contentType:'text/event-stream',body:[msg('next-a','chat','same answer',{meta,role:'assistant'}),msg('next-d','task','',{meta,action:'done'})].map(m=>'data: '+JSON.stringify(m)+'\n\n').join('')});return true;}
  if(path==='/conversation/get-messages'){const cid=body.conversation_id,runID=cid==='a'?aRun:'run-b',meta={conversation_id:cid,run_id:runID,model_version:'model-a',model_profile:'profile-one'};await json([msg(runID+'-u1','chat','same prompt',{meta}),msg(runID+'-a2','chat','same answer',{meta,role:'assistant'}),msg(runID+'-d3','task','',{meta,action:'done'})]);return true;}
  if(path==='/chat/followups'){if(body.run_id==='run-a')await gate;await json({suggestions:['Suggestion for '+body.run_id]});return true;}
 }});
 const pending=a.page.waitForRequest(r=>new URL(r.url()).pathname.endsWith('/chat/followups'));await a.select('a');await pending;await a.select('b');await a.page.getByRole('button',{name:'Suggestion for run-b',exact:true}).waitFor();
 const response=a.page.waitForResponse(r=>new URL(r.url()).pathname.endsWith('/chat/followups')&&r.request().postDataJSON().run_id==='run-a');release();await (await response).finished();assert.equal(await a.page.getByRole('button',{name:'Suggestion for run-a',exact:true}).count(),0);await a.select('a');await a.page.getByRole('button',{name:'Suggestion for run-a',exact:true}).waitFor();
 await a.page.locator('textarea').fill('same prompt');await a.page.locator('textarea').press('Enter');await a.page.getByRole('button',{name:'Suggestion for run-a-next',exact:true}).waitFor();assert.equal(await a.page.getByRole('button',{name:'Suggestion for run-a',exact:true}).count(),0);
 assert.deepEqual(a.requests.filter(r=>r.path==='/chat/followups').map(r=>[r.body.conversation_id,r.body.run_id]),[['a','run-a'],['b','run-b'],['a','run-a-next']]);a.check();
});

test('automatic followups retry terminal settlement conflicts within a fixed bound', async t => {
 let calls=0;const a=await app(t,{history:[msg('u1','chat','question'),msg('a2','chat','answer',{role:'assistant',meta:{conversation_id:'a',run_id:'run-a',model_version:'model-a',model_profile:'profile-one'}}),msg('d3','task','',{action:'done'})],handle:async({path,route,json})=>{
  if(path==='/chat/followups'){calls++;if(calls===1)await route.fulfill({status:409,json:{code:409,msg:'run is still running'}});else await json({suggestions:['Ready after settlement']});return true;}
 }});await a.select('a');await a.page.getByRole('button',{name:'Ready after settlement',exact:true}).waitFor();assert.equal(calls,2);await a.select('b');await a.select('a');await a.page.getByRole('button',{name:'Ready after settlement',exact:true}).waitFor();assert.equal(calls,2);a.check();
});

test('automatic suggestion sends omit retired budget fields',async t=>{
 const a=await app(t,{history:[msg('u1','chat','question'),msg('a2','chat','answer',{role:'assistant',meta:{conversation_id:'a',run_id:'run-a',model_version:'model-a',model_profile:'profile-one'}}),msg('d3','task','',{action:'done'})]});await a.select('a');await openBudget(a);const sent=a.page.waitForRequest(r=>new URL(r.url()).pathname.endsWith('/chat/run'));await a.page.getByRole('button',{name:'Follow up once',exact:true}).click();assert.equal((await sent).postDataJSON().budget,undefined);a.check();
});

test('narrow workbench preserves long metrics, acceptance and file information', async t => {
 const long='long-evidence-'.repeat(30),path=long+'.txt';
 const a=await app(t,{history:[msg('u1','chat','Inspect evidence'),msg('a2','chat','Done',{role:'assistant',meta:{conversation_id:'a',run_id:'run-a',model_version:long}}),msg('d3','task','',{action:'done'})],runs:[record({status:'done',resumable:false})],handle:async({path:apiPath,json})=>{
  if(apiPath==='/run/run-a/budget'){await json({run_id:'run-a',budget_run_id:long,status:'done',limits:{max_tokens:2000000,max_wall_seconds:7200,max_steps:300},used_tokens:110,remaining_tokens:1999890,deadline:long,sources:[]});return true;}
  if(apiPath==='/run/acceptance'){await json({contract:{run_id:'run-a',requests:[long]},checks:[{at:'2026-09-15',spec_path:path,report:{ok:false,scope:long,results:[{id:'long',description:long,method:'manual',status:'unverified',actual:long,expected:long,detail:long}]}}]});return true;}
  if(apiPath==='/delivery/list'){await json({deliveries:[{version:1,conversation_id:'a',run_id:'run-a',created_at:1,files:[{path,size:123,sha256:'a'.repeat(64)}]}]});return true;}
  if(apiPath==='/file/list'){await json([{name:path,dir:false,size:123}]);return true;}
 }});await a.select('a');await openMetrics(a);await a.page.setViewportSize({width:693,height:1000});await a.page.getByRole('separator',{name:'调整工作台宽度'}).press('Home');await a.page.getByRole('button',{name:'用量明细',exact:true}).click();await a.page.getByText('共享运行：'+long,{exact:true}).waitFor();
 const fits=async locator=>assert.equal(await locator.evaluate(el=>el.scrollWidth<=el.clientWidth+1),true);
 await fits(a.page.getByRole('region',{name:'当前运行统计'}));await fits(a.page.getByRole('region',{name:'运行用量',exact:true}));
 await openWorkbench(a,'运营台');await a.page.getByRole('button',{name:'验收证据',exact:true}).click();await a.page.getByText('实际：'+long,{exact:true}).waitFor();await fits(a.page.getByRole('region',{name:'运行验收记录'}));
 await openWorkbench(a,'成果');await a.page.getByRole('button',{name:path,exact:true}).waitFor();await a.page.getByRole('button',{name:'交付快照',exact:true}).click();const link=a.page.getByRole('link',{name:'下载快照 '+path,exact:true});await link.waitFor();assert.ok((await link.boundingBox()).height<=28);await fits(link);await fits(a.page.getByRole('region',{name:'交付快照列表'}));
 if(process.env.ORKA_CAPTURE_SCREENSHOTS==='1')await a.page.screenshot({path:root+'tests/screenshots/workbench-long-files.png'});a.check();
});

test('settlement conflict gets one delayed retry only while the answer remains mounted',async t=>{
 let calls=0;const history=[msg('u1','chat','question'),msg('a2','chat','answer',{role:'assistant',meta:{conversation_id:'a',run_id:'run-a',model_version:'model-a',model_profile:'profile-one'}}),msg('d3','task','',{action:'done'})];
 const a=await app(t,{history,handle:async({path,json,route})=>{if(path==='/chat/followups'){calls++;if(calls<=3)await route.fulfill({status:409,json:{code:409,msg:'settling'}});else await json({suggestions:['Settled on final retry']});return true;}}});
 await a.select('a');await a.page.getByRole('button',{name:'Settled on final retry',exact:true}).waitFor({timeout:8500});assert.equal(calls,4);
 let cancelled=0;const b=await app(t,{history,handle:async({path,route})=>{if(path==='/chat/followups'){cancelled++;await route.fulfill({status:409,json:{code:409,msg:'settling'}});return true;}}});
 const third=b.page.waitForResponse(r=>new URL(r.url()).pathname.endsWith('/chat/followups')&&cancelled===3);await b.select('a');await third;await b.select('b');await b.page.waitForTimeout(5500);assert.equal(cancelled,3);a.check();b.check();
});

test('existing tool picker separates DOM browser from visual GUI without authorizing code',async t=>{
 const a=await app(t,{handle:async({path,json,route})=>{
  if(path==='/tools/catalog'){await json([{name:'browser',group:'browser',description:'DOM webpage actions',danger:true},{name:'gui_agent',group:'gui_agent',description:'Visual page actions',danger:true},{name:'python',group:'code',description:'Python execution',danger:true}]);return true;}
  if(path==='/chat/run'){await route.fulfill({status:503,json:{code:503,msg:'fixture rejection'}});return true;}
 }});await a.select('a');await a.page.getByRole('button',{name:'工具范围'}).click();const dom=a.page.getByRole('button',{name:'网页 DOM',exact:true}),gui=a.page.getByRole('button',{name:'GUI 视觉',exact:true});await dom.waitFor();await gui.waitFor();assert.match(await dom.getAttribute('title'),/DOM/);assert.match(await gui.getAttribute('title'),/截图/);
 await dom.click();await a.page.getByRole('button',{name:'工具范围'}).click();let sent=a.page.waitForRequest(r=>new URL(r.url()).pathname.endsWith('/chat/run'));await a.page.locator('textarea').fill('DOM only');await a.page.locator('textarea').press('Enter');assert.deepEqual((await sent).postDataJSON().enabled_tools,['browser']);await a.page.getByText('发送失败',{exact:false}).first().waitFor();
 await a.page.getByRole('button',{name:'工具范围'}).click();await dom.click();await gui.click();await a.page.reload();await a.select('a');await a.page.getByRole('button',{name:'工具范围'}).click();assert.equal(await dom.getAttribute('aria-pressed'),'false');assert.equal(await gui.getAttribute('aria-pressed'),'true');await a.page.getByRole('button',{name:'工具范围'}).click();sent=a.page.waitForRequest(r=>new URL(r.url()).pathname.endsWith('/chat/run'));await a.page.locator('textarea').fill('Visual GUI only');await a.page.locator('textarea').press('Enter');assert.deepEqual((await sent).postDataJSON().enabled_tools,['gui_agent']);assert.equal(await a.page.locator('header').getByRole('button',{name:/网页 DOM|GUI 视觉/}).count(),0);a.check();
});

test('one composer control switches with input and steers the live run without a second run', async t => {
 let finish;
 const completion = new Promise(resolve => { finish=resolve; });
 t.after(()=>finish());
 let fail=true;
 const a=await app(t,{handle:async({path,body,route,json})=>{
  if(path==='/chat/run') {
   await route.fulfill({contentType:'text/event-stream',body:`id: 1\ndata: ${JSON.stringify(msg('start1','task','',{action:'start'}))}\n\n`});return true;
  }
  if(path==='/chat/attach') {
   await completion;
   await route.fulfill({contentType:'text/event-stream',body:`id: 3\ndata: ${JSON.stringify(msg('done3','task','',{action:'done'}))}\n\n`}).catch(()=>{});return true;
  }
  if(path==='/chat/steer') {
   if(fail) await route.fulfill({status:503,json:{code:503,msg:'temporary failure'}});
   else await json({run_id:body.run_id,message:msg('steer2','chat',body.message)});
   return true;
  }
 }});
 await a.select('a');
 const send=a.page.getByRole('button',{name:'发送',exact:true}), stop=a.page.getByRole('button',{name:'停止',exact:true}), input=a.page.locator('textarea');
 assert.equal(await send.count(),1);assert.equal(await stop.count(),0);assert.equal(await send.isDisabled(),true);
 await input.fill('long running task');await send.click();await stop.waitFor();
 assert.equal(await send.count(),0);
 await input.fill('change the plan now');await send.waitFor();assert.equal(await stop.count(),0);
 await input.fill('   ');await stop.waitFor();assert.equal(await send.count(),0);
 await input.fill('change the plan now');await send.click();
 await a.page.getByText('temporary failure',{exact:false}).first().waitFor();assert.equal(await input.inputValue(),'change the plan now');
 fail=false;await send.click();await stop.waitFor();assert.equal(await input.inputValue(),'');
 const submissions=a.requests.filter(r=>r.path==='/chat/steer');
 assert.equal(submissions.length,2);assert.equal(submissions[0].body.request_id,submissions[1].body.request_id);assert.equal(submissions[1].body.run_id,'run-a');
 assert.equal(a.requests.filter(r=>r.path==='/chat/run').length,1);
 assert.equal(a.requests.filter(r=>r.path==='/chat/kill').length,0);
 await a.page.locator('input[type=file]').setInputFiles({name:'extra.txt',mimeType:'text/plain',buffer:Buffer.from('new data')});
 await a.page.getByRole('button',{name:'移除 extra.txt'}).waitFor();await send.waitFor();assert.equal(await stop.count(),0);
 await send.click();await stop.waitFor();
 assert.ok(a.requests.filter(r=>r.path==='/chat/steer').at(-1).body.file_ids.length);
 finish();await send.waitFor();assert.equal(await stop.count(),0);a.check();
});


test('home scatter cards select without sending and keep mobile composer usable',async t=>{
 const a=await app(t); await a.page.getByRole('region',{name:'Orka 首页'}).waitFor();
 await a.page.getByRole('button',{name:'查看浏览器示例',exact:true}).click();
 assert.equal(await a.page.getByRole('button',{name:'浏览器：打开网页，带回关键信息'}).getAttribute('aria-pressed'),'true');
 assert.equal(a.requests.filter(r=>r.path==='/chat/run').length,0);
 await a.page.getByRole('button',{name:'使用这个示例',exact:true}).click();
 assert.match(await a.page.locator('textarea').inputValue(), /Hacker News/);
 assert.equal(a.requests.filter(r=>r.path==='/chat/run').length,0,'example only fills the draft');
 await a.page.getByRole('button',{name:'查看深度调研示例',exact:true}).click();
 if(process.env.ORKA_HOME_SCREENSHOT) await a.page.screenshot({path:process.env.ORKA_HOME_SCREENSHOT,animations:"disabled"});
 await a.page.getByRole('button',{name:'切换侧栏',exact:true}).click();
 await a.page.setViewportSize({width:390,height:844});
 await a.page.emulateMedia({reducedMotion:'reduce'});
 const box=await a.page.locator('textarea').boundingBox(); assert.ok(box.width>250,'mobile text area is not squeezed by icons');
 assert.ok(await a.page.getByRole('button',{name:'发送',exact:true}).isVisible());
 const stage=a.page.getByRole('region',{name:'任务示例'}); await stage.scrollIntoViewIfNeeded();
 assert.ok(await a.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'no horizontal page overflow');
 if(process.env.ORKA_HOME_MOBILE_SCREENSHOT) await a.page.screenshot({path:process.env.ORKA_HOME_MOBILE_SCREENSHOT,animations:"disabled"});
 await a.page.setViewportSize({width:1440,height:1000});await a.page.getByRole('button',{name:'切换到暗色模式'}).click();
 if(process.env.ORKA_HOME_DARK_SCREENSHOT) await a.page.screenshot({path:process.env.ORKA_HOME_DARK_SCREENSHOT,animations:"disabled"});
 a.check();
});

test('resume uses tools selected in its own conversation',async t=>{
 const a=await app(t,{runs:[record()],handle:async({path,json})=>{
  if(path==='/tools/catalog'){await json([{name:'python',description:'Run Python',group:'code',dangerous:true},{name:'fetch_url',description:'Read web',group:'web'}]);return true;}
 }});
 await a.select('a');
 await a.page.getByRole('button',{name:'工具范围',exact:true}).click();
 await a.page.getByRole('button',{name:'代码',exact:true}).click();
 await a.page.getByRole('button',{name:'收起',exact:true}).click();
 await a.select('b');
 await a.page.getByRole('button',{name:'切换工作台面板'}).click();
 await a.page.getByRole('tab',{name:'运营台',exact:false}).first().click();
 await a.page.getByRole('button',{name:'继续任务',exact:true}).click();
 await a.page.getByText('服务未确认该任务继续运行',{exact:false}).first().waitFor();
 const sent=a.requests.find(r=>r.path==='/chat/resume_run');
 assert.deepEqual(sent.body.enabled_tools,['code']);a.check();
});

test('starting a new run does not animate the previous run tool history',async t=>{
 let finish; const completion=new Promise(resolve=>{finish=resolve});t.after(()=>finish());
 const a=await app(t,{history:[msg('u1','chat','old task'),msg('t2','tool','',{action:'call',payload:{tool:'file_read',args:{path:'old.txt'},result:'old result'}}),msg('a3','chat','old answer',{role:'assistant'}),msg('d4','task','',{action:'done'})],handle:async({path,route})=>{
  if(path==='/chat/run'){await route.fulfill({contentType:'text/event-stream',body:`id: 1\ndata: ${JSON.stringify(msg('start5','task','',{action:'start',meta:{conversation_id:'a',run_id:'run-new'}}))}\n\n`});return true;}
  if(path==='/chat/attach'){await completion;await route.fulfill({contentType:'text/event-stream',body:`id: 2\ndata: ${JSON.stringify(msg('done6','task','',{action:'done',meta:{conversation_id:'a',run_id:'run-new'}}))}\n\n`}).catch(()=>{});return true;}
 }});
 await a.select('a');await a.page.getByRole('button',{name:'查看 · 1 步',exact:true}).waitFor();
 await a.page.locator('textarea').fill('new task');await a.page.getByRole('button',{name:'发送',exact:true}).click();
 await a.page.getByRole('button',{name:'停止',exact:true}).waitFor();
 assert.equal(await a.page.getByRole('button',{name:'执行中 · 1 步',exact:true}).count(),0);
 await a.page.getByRole('button',{name:'查看 · 1 步',exact:true}).waitFor();finish();a.check();
});

test('numbered alternatives remain prose, never a completed execution plan',async t=>{
 const a=await app(t,{history:[msg('u1','chat','news'),msg('a2','chat','无法读取指定来源。\n\n后续方案：\n1. 稍后重试\n2. 换用其他来源\n3. 提供原文\n\n请选一种方式。',{role:'assistant'}),msg('d3','task','',{action:'done'})]});
 await a.select('a');await a.page.getByText('稍后重试',{exact:true}).waitFor();
 assert.equal(await a.page.getByText('已完成',{exact:true}).count(),0);
 assert.equal(await a.page.getByText('🗂️ 执行计划',{exact:true}).count(),0);a.check();
});

test('live overview uses ledger while run counters remain zero; terminal archive is downloadable', async t => {
 const a=await app(t,{runs:[record({status:'running',tokens:0,tool_calls:0})],history:[msg('tool1','tool','',{payload:{tool:'shell',args:{command:'build'},result:JSON.stringify({ok:true,exit_code:0,stdout:'',stderr:'',file_changes:{paths:['bundle.zip'],partial:false}})}})],handle:async({path,json})=>{
  if(path==='/run/run-a/budget'){await json({run_id:'run-a',budget_run_id:'run-a',status:'running',used_tokens:12345,reserved_tokens:500,unknown_calls:0,unknown_tokens:0,estimated_tokens:0,used_steps:2,sources:[]});return true;}
  if(path==='/file/list'){await json([{name:'bundle.zip',dir:false,size:10}]);return true;}
 }});
 await a.select('a');
 const download=a.page.getByRole('link',{name:'下载 bundle.zip',exact:true});await download.waitFor();
 const href=new URL(await download.getAttribute('href'),origin);assert.equal(href.searchParams.get('conversation_id'),'a');assert.equal(href.searchParams.get('path'),'bundle.zip');
 await openMetrics(a);await a.page.getByText('累计用量 12345 tokens（含未知调用的保守占用） · 在途 500',{exact:true}).waitFor();
 assert.equal(await a.page.getByText('0 tokens · 0 次工具调用',{exact:true}).count(),0);a.check();
});


test('failed process receipt is displayed as failed even without legacy error field', async t => {
 const result='tool error (shell, recoverable — adjust the arguments, try another tool, or proceed without this result): tool "shell" error: '+JSON.stringify({ok:false,exit_code:7,stdout:'',stderr:'trace'})+'\n[Execution evidence id: exit_code=7.]';
 const a=await app(t,{history:[msg('t1','tool','',{payload:{tool:'shell',args:{command:'verify'},result}})]});
 await a.select('a');await a.page.getByRole('button',{name:'查看 · 1 步',exact:true}).click();
 await a.page.getByTitle('失败',{exact:true}).waitFor();await a.page.getByText('执行失败（退出码 7）',{exact:false}).waitFor();a.check();
});

test('sandbox Markdown downloads survive URL sanitizing without enabling unsafe links', async t => {
 const content='下载：[最终包](sandbox:?path=/workspace/report.zip)\n\n[普通链接](https://example.org/docs) [危险链接](javascript:alert%281%29) [越界链接](sandbox:/workspace/../other.zip)';
 const a=await app(t,{history:[msg('a1','chat',content,{role:'assistant'}),msg('d2','task','',{action:'done'})]});
 await a.select('a');
 const link=a.page.getByRole('link',{name:'最终包',exact:false});await link.waitFor();
 const url=new URL(await link.getAttribute('href'),origin);
 assert.equal(url.pathname,BASE+'/file/download');assert.equal(url.searchParams.get('conversation_id'),'a');assert.equal(url.searchParams.get('path'),'report.zip');
 assert.equal(await a.page.getByRole('link',{name:'普通链接',exact:true}).getAttribute('href'),'https://example.org/docs');
 for(const name of ['危险链接','越界链接'])assert.equal(await a.page.getByRole('link',{name,exact:true}).getAttribute('href'),'');
 a.check();
});

test('declared release remains downloadable without expanding intermediate files', async t => {
 const history=Array.from({length:12},(_,i)=>msg(`t${i}`,'tool','',{payload:{tool:'file_write',args:{path:`source-${i}.py`},result:'saved successfully'}}));
 history.push(msg('p20','plan','',{payload:{outputs:['release.zip','validation.md'],steps:[]}}));
 history.push(msg('u21','chat','补充发布回归',{role:'user',action:'human_input'}));
 const a=await app(t,{history,handle:async({path,json})=>{
  if(path==='/file/list'){await json([...Array.from({length:12},(_,i)=>({name:`source-${i}.py`,dir:false,size:10})),{name:'release.zip',dir:false,size:100}]);return true;}
 }});
 await a.select('a');await a.page.getByRole('link',{name:'下载 release.zip',exact:true}).waitFor();
 assert.equal(await a.page.getByRole('link',{name:'下载 validation.md',exact:true}).count(),0);
 await a.page.getByRole('button',{name:'查看全部 13 个文件',exact:true}).waitFor();a.check();
});

test('execution evidence exposes stale file versions without claiming acceptance', async t => {
 const a=await app(t,{runs:[record()],handle:async({path,json})=>{
  if(path==='/run/acceptance'){await json({contract:{run_id:'run-a',requests:[]},checks:[],executions:[{id:'exec-1',run_id:'run-a',at:1,tool:'shell',command:'run tests',ok:true,exit_code:0,stdout:'OK',stderr:'',files:[{path:'bundle.zip',sha256:'old-sha'}],changed_since:['bundle.zip'],revisions_partial:true}]});return true;}
 }});
 await a.select('a');await openWorkbench(a,'运营台');await a.page.getByRole('button',{name:'验收证据',exact:true}).click();await a.page.getByText('实际执行记录 · 1',{exact:true}).click();
 await a.page.getByText('旧结果不能作为当前版本的验收结论。',{exact:false}).waitFor();
 await a.page.getByText('文件版本记录不完整，不能据此判定全部文件未变化。',{exact:true}).waitFor();
 assert.equal(await a.page.getByText('检查通过',{exact:true}).count(),0);a.check();
});

test('many produced files stay compact but can all be expanded and downloaded',async t=>{
 const paths=Array.from({length:20},(_,i)=>`report-${i}.txt`);
 const a=await app(t,{history:[msg('t1','tool','',{payload:{tool:'shell',args:{command:'build'},result:JSON.stringify({ok:true,exit_code:0,stdout:'',stderr:'',file_changes:{paths,partial:false}})}})],handle:async({path,json})=>{
  if(path==='/file/list'){await json(paths.map(name=>({name,dir:false,size:1})));return true;}
 }});
 await a.select('a');await a.page.getByRole('button',{name:'查看全部 20 个文件',exact:true}).waitFor();
 assert.equal(await a.page.getByRole('link',{name:/^下载 report-/}).count(),8);
 await a.page.getByRole('button',{name:'查看全部 20 个文件',exact:true}).click();assert.equal(await a.page.getByRole('link',{name:/^下载 report-/}).count(),20);
 await a.page.getByRole('button',{name:'收起文件',exact:true}).click();assert.equal(await a.page.getByRole('link',{name:/^下载 report-/}).count(),8);a.check();
});

test('historical run usage loads only when its detail is opened',async t=>{
 let reads=[];
 const a=await app(t,{runs:Array.from({length:20},(_,i)=>record({run_id:`history-${i}`,status:'done'})),handle:async({path,json})=>{
  if(/^\/run\/history-\d+\/budget$/.test(path)){reads.push(path);await json({run_id:path.split('/')[2],used_tokens:1,reserved_tokens:0,status:'done',sources:[]});return true;}
 }});
 await a.select('a');await openWorkbench(a,'运营台');await a.page.getByRole('button',{name:'用量明细',exact:true}).first().waitFor();assert.equal(reads.length,0);
 await a.page.getByRole('button',{name:'用量明细',exact:true}).first().click();await a.page.getByText('已用（含保守占用）1 · 预留 0 tokens',{exact:true}).waitFor();
 assert.equal(reads.length,1);a.check();
});

test('workspace provenance stays distinct from declared deliverables and refusal reason stays visible',async t=>{
 const a=await app(t,{runs:[record()],handle:async({path,json})=>{
  if(path==='/run/acceptance'){await json({contract:{run_id:'run-a',requests:[]},checks:[],executions:[{id:'e1',run_id:'run-a',tool:'shell',at:1,ok:false,exit_code:-1,stdout:'',stderr:'',error:'command refused',command:'verify',revision_scope:'workspace_sample',files:[{path:'report.zip',sha256:'observed-sha'}]}]});return true;}
 }});
 await a.select('a');await openWorkbench(a,'运营台');await a.page.getByRole('button',{name:'验收证据',exact:true}).click();await a.page.getByText('实际执行记录 · 1',{exact:true}).click();
 await a.page.getByText('工作区文件抽样，未登记交付清单；这些文件不代表正式交付物，也不代表全部被本命令验证。',{exact:true}).waitFor();
 await a.page.getByText('command refused',{exact:true}).waitFor();assert.equal(await a.page.getByText('检查通过',{exact:true}).count(),0);a.check();
});
