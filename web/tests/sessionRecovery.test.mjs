import { createRequire } from 'node:module';
import { join } from 'node:path';
import { test } from 'node:test';
import assert from 'node:assert/strict';
const require=createRequire(import.meta.url);
const {SessionRecoveryStore,clearSessionRecovery,SESSION_PREFIX,SESSION_TTL_MS,SESSION_ENTRY_BYTES,SESSION_TOTAL_BYTES,SESSION_MAX_ENTRIES}=require(join(process.env.ORKA_TEST_BUILD,'lib/sessionRecovery.js'));
class MemoryStorage {
 data=new Map(); get length(){return this.data.size;} key(i){return [...this.data.keys()][i]??null;}
 getItem(k){return this.data.get(k)??null;} setItem(k,v){this.data.set(k,v);} removeItem(k){this.data.delete(k);}
}
const draft=(text='draft',cid='a')=>({text,attachments:[{name:'input.txt',path:'input.txt',image:false,conversationID:cid}],budget:{max_tokens:1234}});
const retry={message:'original',conversationID:'a',userEmail:'owner',enabledTools:['code'],fileIDs:['input.txt'],modelProfile:'opaque-revision',selectedVersion:'model-a',activeSkill:'coder',confirmRisky:true,budget:{max_tokens:1234}};
test('session records isolate conversation and owner, strip credential fields and survive new handles',()=>{
 const storage=new MemoryStorage();const s=new SessionRecoveryStore('owner',storage);s.writeDraft('a',draft());s.writeRetry('a',{...retry,api_key:'must-not-persist',token:'must-not-persist',onAccepted:()=>{}});const restored=new SessionRecoveryStore('owner',storage);assert.deepEqual(restored.readRetry('a'),retry);assert.deepEqual(restored.readDraft('a'),draft());assert.equal(restored.readDraft('b'),undefined);assert.ok(!JSON.stringify([...storage.data]).includes('must-not-persist'));
 const other=new SessionRecoveryStore('other',storage);assert.equal(other.readRetry('a'),undefined);assert.equal(s.writeRetry('a',retry),false);assert.equal(other.readDraft('a'),undefined);
});
test('logout removes only application recovery entries and invalidates pending writers',()=>{
 const storage=new MemoryStorage();storage.setItem('unrelated','keep');const s=new SessionRecoveryStore('owner',storage);s.writeRetry('a',retry);clearSessionRecovery(storage);assert.equal(s.writeRetry('a',retry),false);assert.deepEqual([...storage.data],[['unrelated','keep']]);
});
test('expired, future, wrong-schema and corrupt records are discarded rather than restored',()=>{
 const storage=new MemoryStorage();let now=1000;const s=new SessionRecoveryStore('owner',storage,()=>now);s.writeDraft('a',draft());now+=SESSION_TTL_MS;assert.equal(s.readDraft('a'),undefined);
 for(const change of [e=>({...e,version:999}),e=>({...e,savedAt:now+100}),e=>({...e,owner:'foreign'}),e=>({...e,payload:{...e.payload,attachments:[{...e.payload.attachments[0],conversationID:'b'}]}})]){
  s.writeDraft('a',draft());const key=[...storage.data.keys()].find(k=>k.endsWith(':draft'));storage.setItem(key,JSON.stringify(change(JSON.parse(storage.getItem(key)))));assert.equal(s.readDraft('a'),undefined);
 }
 s.writeDraft('a',draft());const key=[...storage.data.keys()].find(k=>k.endsWith(':draft'));storage.setItem(key,'{broken');assert.equal(s.readDraft('a'),undefined);
});
test('oversize and quota failures never revive stale snapshots; total and count stay bounded',()=>{
 const storage=new MemoryStorage();const s=new SessionRecoveryStore('owner',storage);s.writeDraft('a',draft('old'));assert.equal(s.writeDraft('a',draft('x'.repeat(SESSION_ENTRY_BYTES))),false);assert.equal(s.readDraft('a'),undefined);assert.ok(s.getWarning());
 for(let i=0;i<50;i++)s.writeDraft(String(i),draft('x'.repeat(100000),String(i)));const entries=[...storage.data].filter(([k])=>k!==SESSION_PREFIX+'owner');assert.ok(entries.length<=SESSION_MAX_ENTRIES);assert.ok(entries.reduce((sum,[k,v])=>sum+Buffer.byteLength(k)+Buffer.byteLength(v),0)<=SESSION_TOTAL_BYTES);
 s.writeRetry('a',retry);storage.setItem=()=>{throw new Error('quota');};assert.equal(s.writeRetry('a',{...retry,message:'new'}),false);assert.equal(s.readRetry('a'),undefined);
});
