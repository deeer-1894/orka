import { useEffect, useRef, useState } from 'react';
import { modelProfiles } from '../api';
import { profileInput, type Capability, type ModelProfile, type ModelProfiles } from '../lib/modelProfiles';
export function useModelProfiles() {
 const [data,setData]=useState<ModelProfiles>({profiles:[],active_profile_id:''});
 const [loaded,setLoaded]=useState(false),[busy,setBusy]=useState(''),[error,setError]=useState(''),[notice,setNotice]=useState(''),[revision,setRevision]=useState(0);
 const [dirty,setDirty]=useState(false);const alive=useRef(true);
 useEffect(()=>{alive.current=true;setLoaded(false);setBusy('load');setError('');
  modelProfiles.get().then(d=>{if(alive.current){setData(d);setLoaded(true);setDirty(false);}}).catch(e=>{if(alive.current)setError(e.message);}).finally(()=>{if(alive.current)setBusy('');});
  return()=>{alive.current=false;};
 },[revision]);
 const edit=(fn:(data:ModelProfiles)=>ModelProfiles)=>{setData(fn);setDirty(true);setNotice('');};
 const patch=(id:string,update:Partial<ModelProfile>)=>edit(d=>({...d,profiles:d.profiles.map(p=>p.id===id?{...p,...update,...(('base_url'in update||'protocol'in update||'api_key'in update)?{verified:{}}:{})}:p)}));
 const save=async()=>{if(!loaded||busy)return false;setBusy('save');setError('');try{const saved=await modelProfiles.save({active_profile_id:data.active_profile_id,profiles:data.profiles.map(profileInput)});if(alive.current){setData(saved);setDirty(false);setNotice('连接配置已保存');}return true;}catch(e){if(alive.current)setError((e as Error).message);return false;}finally{if(alive.current)setBusy('');}};
 const discover=async(p:ModelProfile)=>{if(busy)return;setBusy('discover');setError('');try{const r=await modelProfiles.discover({profile_id:p.id,protocol:p.protocol,provider:p.provider,base_url:p.base_url,...(p.api_key?{api_key:p.api_key}:{})});if(alive.current){patch(p.id,{models:[...new Set([...p.models,...r.models])]});setNotice(r.notice||(r.source==='preset'?'预设候选尚未验证权限':'已发现模型，尚未验证调用能力'));}}catch(e){if(alive.current)setError((e as Error).message);}finally{if(alive.current)setBusy('');}};
 const probe=async(p:ModelProfile,model:string,capability:Capability)=>{if(busy||dirty||!loaded)return;setBusy('probe');setError('');try{const checks=await modelProfiles.probe({profile_id:p.id,model,capabilities:[capability]});if(alive.current)setData(d=>({...d,profiles:d.profiles.map(x=>x.id===p.id?{...x,verified:{...x.verified,[model]:{...x.verified?.[model],...checks}}}:x)}));}catch(e){if(alive.current)setError((e as Error).message);}finally{if(alive.current)setBusy('');}};
 return {data,loaded,busy,error,notice,dirty,patch,edit,save,discover,probe,reload:()=>setRevision(n=>n+1)};
}
