import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { join } from 'node:path';
const require=createRequire(import.meta.url);
const {importProfiles,exportProfiles,profileInput}=require(join(process.env.ORKA_TEST_BUILD,'lib/modelProfiles.js'));
test('named profile roundtrip preserves public policies but strips credentials and verification evidence',()=>{
 const policy={model:{first_max_tokens:64,max_tokens:128,timeout_seconds:90,reasoning_effort:'low'}};
 const raw={active_profile_id:'one',profiles:[{id:'one',name:'One',protocol:'openai-compatible',provider:'custom',base_url:'https://example.invalid/v1',models:['model'],enabled:true,api_key:'test-secret',verified:{model:{text:{verified:true}}},policies:policy}]};
 const imported=importProfiles(JSON.stringify(raw));assert.deepEqual(imported.profiles[0].policies,policy);assert.equal(imported.profiles[0].api_key,undefined);assert.equal(imported.profiles[0].verified,undefined);
 assert.deepEqual(JSON.parse(exportProfiles(raw)).profiles[0].policies,policy);assert.ok(!exportProfiles(raw).includes('test-secret'));assert.ok(!('verified' in profileInput(raw.profiles[0])));
});
test('legacy model import remains credential-free and preserves default model order',()=>{
 const result=importProfiles(JSON.stringify({provider:'custom',base_url:'https://example.invalid/v1',model:'default',models:['other'],enabled:true,api_key:'test-secret'}));assert.deepEqual(result.profiles[0].models,['default','other']);assert.equal(result.profiles[0].api_key,undefined);assert.equal(result.active_profile_id,result.profiles[0].id);
});
