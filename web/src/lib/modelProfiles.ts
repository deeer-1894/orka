import { readPolicies, type ModelPolicy } from './modelPolicies';
import { importModelProfile } from './modelSettings';
export type Capability = 'text' | 'vision' | 'tools';
export interface ModelCheck { verified: boolean; checked_at: string; error?: string }
export type ModelVerification = Partial<Record<Capability, ModelCheck>>;
export interface ModelProfile {policies?:Record<string,ModelPolicy>;  id:string; name:string; protocol:string; provider:string; base_url:string; models:string[]; enabled:boolean; api_key_set:boolean; api_key?:string; verified?:Record<string,ModelVerification> }
export interface ModelProfiles { profiles:ModelProfile[]; active_profile_id:string }
export function profileInput(profile:ModelProfile) {
 const {id,name,protocol,provider,base_url,models,enabled,api_key,policies}=profile;
 return {id,name,protocol,provider,base_url:base_url.trim(),models:[...new Set(models.map(m=>m.trim()).filter(Boolean))],enabled,...(policies?{policies}:{}),...(api_key?{api_key}:{})};
}
export function exportProfiles(data:ModelProfiles) {
 return JSON.stringify({version:3,active_profile_id:data.active_profile_id,profiles:data.profiles.map(p=>{const {api_key:_,...input}=profileInput(p);return input;})},null,2);
}
export function importProfiles(text:string):ModelProfiles {
 const value=JSON.parse(text);
 if (!Array.isArray(value?.profiles)) {
  const legacy=importModelProfile(text);const id=crypto.randomUUID();
  return {active_profile_id:id,profiles:[{...legacy,id,name:'导入的连接',protocol:'openai-compatible',api_key_set:false}]};
 }
 if (!value.profiles.length) throw new Error('至少需要一个连接');
 const profiles:ModelProfile[]=value.profiles.map((p:Record<string,unknown>)=>{
  const config=importModelProfile(JSON.stringify({...p,version:2}));
  if(typeof p.id!=='string'||!p.id||typeof p.name!=='string'||!p.name.trim())throw new Error('连接需要 id 和名称');
  const protocol=typeof p.protocol==='string'?p.protocol:'openai-compatible';
  if(protocol!=='openai-compatible')throw new Error('当前仅支持 OpenAI 兼容协议');
  const policies=readPolicies(p.policies);
  return {...config,id:p.id,name:p.name,protocol,api_key_set:false,...(policies?{policies}:{})};
 });
 if(new Set(profiles.map(p=>p.id)).size!==profiles.length)throw new Error('连接 ID 不能重复');
 if(!profiles.some(p=>p.id===value.active_profile_id))throw new Error('活动连接不存在');
 return {profiles,active_profile_id:value.active_profile_id};
}
