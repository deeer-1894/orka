export interface ModelPolicy {first_max_tokens?:number;max_tokens?:number;timeout_seconds?:number;reasoning_effort?:string}
export function readPolicies(value:unknown):Record<string,ModelPolicy>|undefined {
 if(value===undefined)return undefined;
 if(!value||typeof value!=='object'||Array.isArray(value))throw new Error('policies 必须是对象');
 const policies:Record<string,ModelPolicy>={};
 for(const [model,v]of Object.entries(value)){
  if(!v||typeof v!=='object'||Array.isArray(v))throw new Error('模型策略必须是对象');
  const policy:ModelPolicy={};
  for(const field of ['first_max_tokens','max_tokens','timeout_seconds']as const){const n=(v as Record<string,unknown>)[field];if(n!==undefined){if(typeof n!=='number'||!Number.isFinite(n)||n<0)throw new Error('模型策略数值无效');policy[field]=n;}}
  const effort=(v as Record<string,unknown>).reasoning_effort;if(effort!==undefined){if(typeof effort!=='string')throw new Error('推理参数无效');policy.reasoning_effort=effort;}
  Object.defineProperty(policies,model,{value:policy,enumerable:true,configurable:true,writable:true});
 }
 return policies;
}
