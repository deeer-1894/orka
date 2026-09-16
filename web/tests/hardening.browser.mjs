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
   default: throw new Error('Unexpected API '+path);
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
test('history resume rejects unconfirmed response and exhausted budget',async t=>{
 const a=await app(t,{runs:[record(),record({run_id:'exhausted',budget_hit:'tokens',conversation_id:'b'})]}); await a.page.getByRole('button',{name:'切换工作台面板'}).click(); await a.page.getByRole('tab',{name:'运营台',exact:false}).first().click();
 await a.page.getByRole('button',{name:'继续任务',exact:true}).first().click(); await a.page.getByText('服务未确认该任务继续运行',{exact:false}).first().waitFor(); assert.equal(a.requests.filter(r=>r.path==='/chat/resume_run').length,1); assert.equal(await a.page.getByRole('button',{name:'继续任务',exact:true}).count(),1); a.check();
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

test('code execution is explicitly enabled without creating a catalog conversation',async t=>{
 const a=await app(t,{handle:async({path,body,json,route})=>{
  if(path==='/tools/catalog'){await json([{name:'python',group:'code',description:'Run Python',danger:true},{name:'file_read',group:'file',description:'Read file',danger:false}]);return true;}
  if(path==='/chat/run'){await route.fulfill({contentType:'text/event-stream',body:[msg('a2','chat','Tool result',{role:'assistant',meta:{conversation_id:body.conversation_id}}),msg('d3','task','',{action:'done',meta:{conversation_id:body.conversation_id}})].map(m=>`data: ${JSON.stringify(m)}\n\n`).join('')});return true;}
 }});await a.page.getByRole('button',{name:'工具范围'}).click();await a.page.getByText('代码执行需主动启用',{exact:false}).waitFor();assert.equal(a.requests.filter(r=>r.path==='/conversation/create-conversation').length,0);
 await a.select('a');await a.page.locator('textarea').fill('no code by default');await a.page.locator('textarea').press('Enter');await a.page.getByText('Tool result',{exact:true}).waitFor();assert.deepEqual(a.requests.find(r=>r.path==='/chat/run').body.enabled_tools,[]);
 await a.page.getByRole('button',{name:'🐍 代码',exact:true}).click();await a.page.locator('textarea').fill('allow code now');await a.page.locator('textarea').press('Enter');await a.page.getByRole('button',{name:'重新生成',exact:true}).waitFor();assert.ok(a.requests.filter(r=>r.path==='/chat/run').at(-1).body.enabled_tools.includes('code'));assert.equal(await a.page.getByRole('dialog').count(),0);a.check();
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

test('budget evidence exposes unknown usage and retry retains original limits',async t=>{
 const a=await app(t,{runs:[record()],handle:async({path,body,json,route})=>{
  if(path==='/run/run-a/budget'){assert.equal(route.request().method(),'GET');await json({run_id:'run-a',budget_run_id:'workflow-parent',status:'partial',limits:{max_tokens:2000000,max_wall_seconds:7200,max_steps:300},used_tokens:110,reserved_tokens:20,remaining_tokens:1999870,unknown_calls:1,unknown_tokens:100,estimated_tokens:30,used_steps:2,deadline:'2026-09-15T18:00:00Z',sources:[{source:'gui',used_tokens:110,reserved_tokens:20,unknown_calls:1,unknown_tokens:100,estimated_tokens:30}]});return true;}
  if(path==='/chat/run'){await route.fulfill({contentType:'text/event-stream',body:[msg('a2','chat','Budget output',{role:'assistant'}),msg('d3','task','',{action:'partial'})].map(m=>`data: ${JSON.stringify(m)}\n\n`).join('')});return true;}
 }});await a.select('a');await openBudget(a);await a.page.getByLabel('Token 上限').fill('1200');await a.page.getByLabel('运行时长上限（秒）').fill('3600');await a.page.getByLabel('轮次上限').fill('40');await a.page.locator('textarea').fill('budgeted task');await a.page.locator('textarea').press('Enter');await a.page.getByText('Budget output',{exact:true}).waitFor();await openMetrics(a);await a.page.getByRole('button',{name:'预算明细'}).click();await a.page.getByText('未知用量：1 次调用，保守占用 100 tokens',{exact:true}).waitFor();await a.page.getByText('共享预算：workflow-parent',{exact:true}).waitFor();await openBudget(a);await a.page.getByLabel('Token 上限').fill('1800');await a.page.getByRole('button',{name:'重新生成',exact:true}).click();await a.page.waitForRequest(r=>new URL(r.url()).pathname.endsWith('/run/list')).catch(()=>{});const sends=a.requests.filter(r=>r.path==='/chat/run');assert.equal(sends.length,2);assert.deepEqual(sends[0].body.budget,{max_tokens:1200,max_wall_seconds:3600,max_steps:40});assert.deepEqual(sends[1].body.budget,sends[0].body.budget);a.check();
});

test('complete product path from named connection and capability to budget, private GUI, delivery and acceptance',async t=>{
 let submitted=false;
 const profile={id:'chain',name:'Verified account',protocol:'openai-compatible',provider:'custom',base_url:'https://example.invalid/v1',models:['model-a'],enabled:true,api_key_set:true,verified:{}};
 const frame='iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=';
 const a=await app(t,{handle:async({path,body,json,route})=>{
  if(path==='/model-profiles/get'){await json({active_profile_id:'chain',profiles:[profile]});return true;}
  if(path==='/model-profiles/save'){assert.equal(body.active_profile_id,'chain');assert.ok(!('verified' in body.profiles[0]));await json({active_profile_id:'chain',profiles:[profile]});return true;}
  if(path==='/model-profiles/probe'){assert.equal(body.profile_id,'chain');assert.deepEqual(body.capabilities,['tools']);await json({tools:{verified:true,checked_at:'2026-09-15T10:00:00Z'}});return true;}
  if(path==='/chat/run'){
   assert.deepEqual(body.budget,{max_tokens:1200});submitted=true;
   const meta={conversation_id:'a',run_id:'run-a',model_version:'model-a',model_profile:'chain-revision'};
   await route.fulfill({contentType:'text/event-stream',body:[msg('b2','browser','',{meta,payload:{data:frame,action:'screenshot'}}),msg('a3','chat','[Final report](final.txt)',{role:'assistant',meta}),msg('d4','task','',{action:'done',meta})].map(m=>`data: ${JSON.stringify(m)}\n\n`).join('')});return true;
  }
  if(path==='/run/list'){await json({runs:submitted?[record({status:'done',resumable:false})]:[]});return true;}
  if(path==='/delivery/list'){await json({deliveries:submitted?[{version:1,conversation_id:'a',run_id:'run-a',created_at:1,files:[{path:'final.txt',size:6,sha256:'frozen-checksum'}]}]:[]});return true;}
  if(path==='/run/acceptance'){await json({contract:{run_id:'run-a',requests:['Produce final report'],truncated:false},checks:[{at:'2026-09-15T10:00:00Z',spec_path:'acceptance.json',report:{ok:true,scope:'delivery',results:[{id:'file',description:'File exists',method:'sha256',file:'final.txt',status:'passed',sha256:'frozen-checksum'}]}}]});return true;}
  if(path==='/delivery/download'){await route.fulfill({headers:{'content-disposition':'attachment; filename="final.txt"'},body:'frozen'});return true;}
 }});
 await a.page.getByRole('button',{name:'模型配置',exact:true}).click();await a.page.getByRole('button',{name:'命名连接与能力检测'}).click();await a.page.getByRole('button',{name:'保存所有连接'}).click();await a.page.getByText('连接配置已保存',{exact:true}).waitFor();await a.page.getByRole('button',{name:'检测 工具调用 model-a'}).click();await a.page.getByText('工具调用：已验证',{exact:false}).waitFor();await a.page.getByRole('button',{name:'关闭命名连接'}).click();await a.page.getByRole('button',{name:'关闭模型配置'}).click();
 await a.select('a');await openBudget(a);await a.page.getByLabel('Token 上限').fill('1200');await a.page.locator('textarea').fill('Complete delivery');await a.page.locator('textarea').press('Enter');await a.page.getByRole('button',{name:'停止',exact:true}).waitFor({state:'hidden'});await a.page.getByText('交付快照 · 版本 1',{exact:true}).waitFor();await a.page.getByRole('button',{name:/查看 · 1 步/}).click();await a.page.getByAltText('本次运行截图').waitFor();assert.equal(await a.page.locator('iframe').count(),0);
 await openWorkbench(a,'运营台');await a.page.getByRole('button',{name:'验收证据',exact:true}).click();await a.page.getByText('检查通过',{exact:true}).waitFor();await openWorkbench(a,'成果');await a.page.getByRole('button',{name:'交付快照',exact:true}).click();await a.page.getByText('当前工作区',{exact:true}).waitFor();const download=a.page.waitForEvent('download');await a.page.getByRole('link',{name:'下载快照 final.txt'}).click();assert.equal((await download).suggestedFilename(),'final.txt');a.check();
});

test('session drafts reload with attachments, budget and conversation-specific model and scope',async t=>{
 const a=await app(t,{handle:async({path,json})=>{if(path==='/tools/catalog'){await json([{name:'python',group:'code',description:'Python',danger:true}]);return true;}}});
 await a.select('a');await a.page.getByRole('button',{name:'选择模型'}).click();await a.page.getByRole('menuitem').filter({hasText:'model-a'}).click();await a.page.getByRole('button',{name:'工具范围'}).click();await a.page.getByRole('button',{name:'🐍 代码',exact:true}).click();await a.page.getByRole('button',{name:'工具范围'}).click();await a.page.locator('textarea').fill('persistent draft A');await a.page.locator('input[type=file]').setInputFiles({name:'reload.txt',mimeType:'text/plain',buffer:Buffer.from('fixture')});await a.page.getByRole('button',{name:'移除 reload.txt'}).waitFor();await openBudget(a);await a.page.getByLabel('Token 上限').fill('1234');await a.select('b');await a.page.locator('textarea').fill('persistent draft B');
 await a.page.reload();await a.select('a');assert.equal(await a.page.locator('textarea').inputValue(),'persistent draft A');await a.page.getByRole('button',{name:'移除 reload.txt'}).waitFor();assert.ok((await a.page.getByRole('button',{name:'选择模型'}).textContent()).includes('model-a'));await openBudget(a);assert.equal(await a.page.getByLabel('Token 上限').inputValue(),'1234');await a.page.locator('textarea').press('Enter');await a.page.getByText('发送失败',{exact:false}).first().waitFor();const sent=a.requests.filter(r=>r.path==='/chat/run').at(-1).body;assert.deepEqual(sent.file_ids,['reload.txt']);assert.deepEqual(sent.enabled_tools,['code']);assert.equal(sent.selected_version,'model-a');assert.equal(sent.budget.max_tokens,1234);
 await a.page.reload();await a.select('a');assert.equal(await a.page.locator('textarea').inputValue(),'persistent draft A');await a.page.getByRole('button',{name:'移除 reload.txt'}).waitFor();await a.select('b');assert.equal(await a.page.locator('textarea').inputValue(),'persistent draft B');a.check();
});

test('complete retry survives reload and remains separate from the newly edited draft',async t=>{
 let history=[];
 const a=await app(t,{handle:async({path,body,json,route})=>{
  if(path==='/conversation/get-messages'){await json(history);return true;}
  if(path==='/tools/catalog'){await json([{name:'python',group:'code',description:'Python',danger:true}]);return true;}
  if(path==='/chat/run'){history=[msg('u1','chat',body.message),msg('a2','chat','Retry across reload',{role:'assistant',meta:{conversation_id:'a',run_id:'run-a',model_version:'model-a',model_profile:'immutable-revision'}}),msg('d3','task','',{action:'partial'})];await route.fulfill({contentType:'text/event-stream',body:history.slice(1).map(m=>`data: ${JSON.stringify(m)}\n\n`).join('')});return true;}
 }});await a.select('a');await a.page.getByRole('button',{name:'选择模型'}).click();await a.page.getByRole('menuitem').filter({hasText:'model-a'}).click();await a.page.getByRole('button',{name:'工具范围'}).click();await a.page.getByRole('button',{name:'🐍 代码',exact:true}).click();await a.page.getByRole('button',{name:'工具范围'}).click();await a.page.locator('textarea').fill('Original exact input');await a.page.locator('input[type=file]').setInputFiles({name:'original.txt',mimeType:'text/plain',buffer:Buffer.from('fixture')});await a.page.getByRole('button',{name:'移除 original.txt'}).waitFor();await openBudget(a);await a.page.getByLabel('Token 上限').fill('1234');await a.page.locator('textarea').press('Enter');await a.page.getByText('Retry across reload',{exact:true}).waitFor();await a.page.locator('textarea').fill('Next unsent draft');await a.page.getByRole('button',{name:'选择模型'}).click();await a.page.getByRole('menuitem').filter({hasText:'Auto'}).click();await a.page.getByLabel('Token 上限').fill('9999');
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

test('budget controls and run statistics live only in existing workbench views',async t=>{
 const runs=[record({status:'done'}),record({run_id:'run-b',conversation_id:'b',status:'done',tokens:77,tool_calls:5})];
 const a=await app(t,{runs,handle:async({path,body,json,route})=>{
  if(path==='/conversation/get-messages'){const cid=body.conversation_id;await json([msg('a1','chat','Saved answer',{role:'assistant',meta:{conversation_id:cid,run_id:cid==='a'?'run-a':'run-b',model_version:'model-a'}})]);return true;}
  if(path==='/run/list'){await json({runs:body.conversation_id?runs.filter(r=>r.conversation_id===body.conversation_id):runs});return true;}
  if(path==='/metrics'){await json({total_tokens:9876});return true;}
  if(path==='/run/run-a/budget'||path==='/run/run-b/budget'){const runID=path.includes('run-a')?'run-a':'run-b';await json({run_id:runID,budget_run_id:runID,status:'done',limits:{max_tokens:runID==='run-a'?1234:2222,max_wall_seconds:7200,max_steps:300},used_tokens:11,reserved_tokens:0,remaining_tokens:1200,unknown_calls:0,unknown_tokens:0,estimated_tokens:0,used_steps:1,deadline:'2026-09-15T18:00:00Z',sources:[]});return true;}
  if(path==='/chat/run'){await route.fulfill({status:503,json:{code:503,msg:'fixture rejected'}});return true;}
 }});await a.select('a');const main=a.page.getByRole('main');assert.equal(await main.getByText('任务预算',{exact:true}).count(),0);assert.equal(await main.getByRole('button',{name:'预算明细'}).count(),0);assert.equal(await a.page.getByTitle('本进程累计 token 用量').count(),0);assert.ok(!(await main.textContent()).includes('次工具调用'));
 await openBudget(a);await a.page.getByLabel('Token 上限').fill('1234');await a.select('b');await openBudget(a);await a.page.getByLabel('Token 上限').fill('2222');await a.select('a');await openBudget(a);assert.equal(await a.page.getByLabel('Token 上限').inputValue(),'1234');await a.page.reload();await a.select('a');await a.page.locator('textarea').fill('Use saved workspace budget');await a.page.locator('textarea').press('Enter');await a.page.getByText('发送失败',{exact:false}).first().waitFor();assert.equal(a.requests.filter(r=>r.path==='/chat/run').at(-1).body.budget.max_tokens,1234);
 await openMetrics(a);await a.page.getByRole('button',{name:'预算明细'}).click();await a.page.getByText('上限：1234 tokens · 7200 秒 · 300 轮',{exact:true}).waitFor();await a.select('b');await a.page.getByRole('button',{name:'预算明细'}).click();await a.page.getByText('上限：2222 tokens · 7200 秒 · 300 轮',{exact:true}).waitFor();assert.equal(await a.page.getByText('上限：1234 tokens · 7200 秒 · 300 轮',{exact:true}).count(),0);assert.deepEqual([...new Set(a.requests.filter(r=>r.path.endsWith('/budget')).map(r=>r.path))],['/run/run-a/budget','/run/run-b/budget']);assert.equal(await main.getByRole('region',{name:'运行预算'}).count(),0);a.check();
});


async function openWorkbench(a, tab) {
 const toggle=a.page.getByRole('button',{name:'切换工作台面板'});
 if(await toggle.getAttribute('aria-pressed')!=='true')await toggle.click();
 await a.page.getByRole('complementary',{name:'工作台'}).getByRole('tab',{name:tab,exact:false}).click();
}
async function openBudget(a) {
 await openWorkbench(a,'概览');
 if(!await a.page.getByLabel('Token 上限').isVisible())await a.page.getByRole('complementary',{name:'工作台'}).getByText('任务预算',{exact:true}).click();
}
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
 await a.page.getByRole('button',{name:'选择模型'}).click();await a.page.getByRole('menuitem').filter({hasText:'model-a'}).click();await a.page.getByRole('button',{name:'工具范围'}).click();await a.page.getByRole('button',{name:'🐍 代码',exact:true}).click();await a.page.getByRole('button',{name:'工具范围'}).click();await a.page.getByTitle(/^高危操作需确认/).click();await openBudget(a);await a.page.getByLabel('Token 上限').fill('1234');await a.page.locator('textarea').fill('Send to the new conversation');const pending=a.page.waitForRequest(r=>new URL(r.url()).pathname.endsWith('/conversation/create-conversation'));await a.page.locator('textarea').press('Enter');await pending;
 await a.select('b');await a.page.locator('textarea').fill('Keep conversation B draft');const sent=a.page.waitForRequest(r=>new URL(r.url()).pathname.endsWith('/chat/run'));release();const body=(await sent).postDataJSON();assert.equal(body.conversation_id,'new');assert.equal(body.message,'Send to the new conversation');assert.equal(body.selected_version,'model-a');assert.deepEqual(body.enabled_tools,['code']);assert.equal(body.confirm_risky,false);assert.equal(body.budget.max_tokens,1234);assert.equal(await a.page.locator('textarea').inputValue(),'Keep conversation B draft');a.check();
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
 await openMetrics(a);const nav=a.page.getByRole('button',{name:'服务状态',exact:true});await nav.scrollIntoViewIfNeeded();await nav.click();await a.page.getByRole('region',{name:'服务状态'}).waitFor();a.check();
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

test('automatic suggestion sends use the current workbench conversation budget',async t=>{
 const a=await app(t,{history:[msg('u1','chat','question'),msg('a2','chat','answer',{role:'assistant',meta:{conversation_id:'a',run_id:'run-a',model_version:'model-a',model_profile:'profile-one'}}),msg('d3','task','',{action:'done'})]});await a.select('a');await openBudget(a);await a.page.getByLabel('Token 上限').fill('1234');const sent=a.page.waitForRequest(r=>new URL(r.url()).pathname.endsWith('/chat/run'));await a.page.getByRole('button',{name:'Follow up once',exact:true}).click();assert.deepEqual((await sent).postDataJSON().budget,{max_tokens:1234});a.check();
});

test('narrow workbench preserves long metrics, acceptance and file information', async t => {
 const long='long-evidence-'.repeat(30),path=long+'.txt';
 const a=await app(t,{history:[msg('u1','chat','Inspect evidence'),msg('a2','chat','Done',{role:'assistant',meta:{conversation_id:'a',run_id:'run-a',model_version:long}}),msg('d3','task','',{action:'done'})],runs:[record({status:'done',resumable:false})],handle:async({path:apiPath,json})=>{
  if(apiPath==='/run/run-a/budget'){await json({run_id:'run-a',budget_run_id:long,status:'done',limits:{max_tokens:2000000,max_wall_seconds:7200,max_steps:300},used_tokens:110,remaining_tokens:1999890,deadline:long,sources:[]});return true;}
  if(apiPath==='/run/acceptance'){await json({contract:{run_id:'run-a',requests:[long]},checks:[{at:'2026-09-15',spec_path:path,report:{ok:false,scope:long,results:[{id:'long',description:long,method:'manual',status:'unverified',actual:long,expected:long,detail:long}]}}]});return true;}
  if(apiPath==='/delivery/list'){await json({deliveries:[{version:1,conversation_id:'a',run_id:'run-a',created_at:1,files:[{path,size:123,sha256:'a'.repeat(64)}]}]});return true;}
  if(apiPath==='/file/list'){await json([{name:path,dir:false,size:123}]);return true;}
 }});await a.select('a');await openMetrics(a);await a.page.setViewportSize({width:693,height:1000});await a.page.getByRole('separator',{name:'调整工作台宽度'}).press('Home');await a.page.getByRole('button',{name:'预算明细',exact:true}).click();await a.page.getByText('共享预算：'+long,{exact:true}).waitFor();
 const fits=async locator=>assert.equal(await locator.evaluate(el=>el.scrollWidth<=el.clientWidth+1),true);
 await fits(a.page.getByRole('region',{name:'当前运行统计'}));await fits(a.page.getByRole('region',{name:'运行预算',exact:true}));
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
 }});await a.select('a');await a.page.getByRole('button',{name:'工具范围'}).click();const dom=a.page.getByRole('button',{name:'🌐 网页 DOM',exact:true}),gui=a.page.getByRole('button',{name:'🖥️ GUI 视觉',exact:true});await dom.waitFor();await gui.waitFor();assert.match(await dom.getAttribute('title'),/DOM/);assert.match(await gui.getAttribute('title'),/截图/);
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
