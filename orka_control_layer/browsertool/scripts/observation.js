function() {
  // Keep only one bounded observation in the isolated document, never globally
  // on the Go engine. Scope resets alongside refs after run/GUI transitions.
  globalThis.__orkaObserve=(result,q,slot)=>{
    const previous=slot.observation;
    const current={url:result.url,title:result.title,snapshot:structuredClone(result.snapshot)};
    slot.observation=current;
    const mask=value=>{
      let text=value;
      for(const secret of slot.redactions){if(secret){text=text.split(secret).join('[redacted]');const normalized=secret.replace(/\s+/g,' ').trim();if(normalized)text=text.split(normalized).join('[redacted]');}}
      return text;
    };
    const bounded=()=>{if(new TextEncoder().encode(JSON.stringify(result)).length>61000&&result.change){result.change={observed:result.change.observed,added:[],removed:[],omitted:true};}return result;};
    const full=()=>{result.snapshot.mode='full';return bounded();};
    if(!previous||previous.url!==current.url){slot.progress={key:'',count:0};return full();}
    const maskTree=value=>typeof value==='string'?mask(value):Array.isArray(value)?value.map(maskTree):value&&typeof value==='object'?Object.fromEntries(Object.entries(value).map(([k,v])=>[k,maskTree(v)])):value;
    const old=maskTree(previous);
    const strip=snapshot=>{const copy={...snapshot};delete copy.id;return copy;};
    const changed=old.title!==current.title||JSON.stringify(strip(old.snapshot))!==JSON.stringify(strip(current.snapshot));
    const before=new Set(old.snapshot.text.split('\n')),after=new Set(current.snapshot.text.split('\n'));
    const added=[...after].filter(x=>!before.has(x)),removed=[...before].filter(x=>!after.has(x));
    result.change={observed:changed,added:added.slice(0,24).map(x=>x.slice(0,240)),removed:removed.slice(0,24).map(x=>x.slice(0,240)),omitted:added.length>24||removed.length>24||added.some(x=>x.length>240)||removed.some(x=>x.length>240)};
    const key=JSON.stringify([q.action,q.ref,q.selector,q.frame||[]]);
    if(['click','fill','select','press','scroll','fill_form'].includes(q.action)){
      const count=changed?0:slot.progress?.key===key?slot.progress.count+1:1;
      slot.progress={key,count};
      result.progress={unchanged_actions:count,note:count>=2?'Repeated action produced no visible change. Inspect loading, blocking layers or login state; do not assume success or resubmit blindly.':changed?'Visible page state changed; verify the intended outcome.':'Action completed without a visible change; this alone does not establish success or failure.'};
    }
    if(q.view==='full'||['open','preview','snapshot'].includes(q.action)||result.snapshot.omitted||old.snapshot.omitted||added.length+removed.length>48||(old.snapshot.text!==current.snapshot.text&&added.length===0&&removed.length===0))return full();
    result.snapshot.mode=changed?'delta':'unchanged';
    // Keep all current controls and their refs; only repeated prose is omitted.
    result.snapshot.text=result.change.added.join('\n');
    return bounded();
  };
}
