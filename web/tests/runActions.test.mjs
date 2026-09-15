import test from 'node:test';
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {join} from 'node:path';
const require=createRequire(import.meta.url);
const {RunActions}=require(join(process.env.ORKA_TEST_BUILD,'lib/runActions.js'));
const run={run_id:'r',conversation_id:'c',status:'partial',resumable:true,created_at:1};
test('accepted resume cannot submit again from a stale history button',async()=>{
 let calls=0;const actions=new RunActions({list:async()=>[run],get:async()=>run,resume:async()=>{calls++;return {resumed:true,conversation_id:'c'};}});
 await actions.resume(run);await actions.resume(run);assert.equal(calls,1);
});
test('unconfirmed resume releases lock and remains retryable',async()=>{
 const actions=new RunActions({list:async()=>[run],get:async()=>run,resume:async()=>({resumed:false,conversation_id:'c'})});await assert.rejects(actions.resume(run),/服务未确认/);assert.equal(actions.isBusy('c'),false);
});
