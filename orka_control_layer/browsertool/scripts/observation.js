function() {
  // Keep a bounded committed baseline and at most one bounded candidate in the
  // isolated document. Failed/racy reads never advance the committed baseline.
  globalThis.__orkaCommitObservation=(slot,id)=>{
    const candidate=slot.pendingObservation;
    if(!candidate||candidate.scope!==slot.scope||id!==slot.nonce||candidate.observation.snapshot.id!==id)return false;
    slot.observation=candidate.observation;
    slot.progress=candidate.progress;
    slot.pendingObservation=null;
    return true;
  };
  globalThis.__orkaObserve=(result,q,slot,coverage)=>{
    // Old helper versions eagerly committed their last (possibly unseen) read.
    if(slot.observationVersion!==2){slot.observation=null;slot.pendingObservation=null;slot.observationVersion=2;}
    const previous=slot.observation;
    const current={url:result.url,title:result.title,snapshot:structuredClone(result.snapshot),coverage};
    const candidate={observation:current,scope:slot.scope,progress:slot.progress||{key:'',count:0}};
    slot.pendingObservation=candidate;
    const mask=value=>{
      let text=value;
      for(const secret of slot.redactions){if(secret){text=text.split(secret).join('[redacted]');const normalized=secret.replace(/\s+/g,' ').trim();if(normalized)text=text.split(normalized).join('[redacted]');}}
      return text;
    };
    const bounded=()=>{if(new TextEncoder().encode(JSON.stringify(result)).length>61000&&result.change){result.change={observed:result.change.observed,added:[],removed:[],omitted:true};}return result;};
    const full=()=>{result.snapshot.mode='full';return bounded();};
    if(!previous||previous.url!==current.url){candidate.progress={key:'',count:0};return full();}
    const maskTree=value=>typeof value==='string'?mask(value):Array.isArray(value)?value.map(maskTree):value&&typeof value==='object'?Object.fromEntries(Object.entries(value).map(([k,v])=>[k,maskTree(v)])):value;
    const old=maskTree(previous);
    const strip=snapshot=>{const copy={...snapshot};delete copy.id;return copy;};
    const rangeChanged=JSON.stringify(previous.coverage)!==JSON.stringify(coverage);
    const changed=rangeChanged||old.title!==current.title||JSON.stringify(strip(old.snapshot))!==JSON.stringify(strip(current.snapshot));
    // A single ordered splice is linear, bounded, and preserves duplicates and
    // mixed reorder/replacement edits. Sets cannot describe these faithfully.
    const before=old.snapshot.text?old.snapshot.text.split('\n'):[],after=current.snapshot.text?current.snapshot.text.split('\n'):[];
    let start=0,endBefore=before.length,endAfter=after.length;
    while(start<endBefore&&start<endAfter&&before[start]===after[start])start++;
    while(endBefore>start&&endAfter>start&&before[endBefore-1]===after[endAfter-1]){endBefore--;endAfter--;}
    const added=after.slice(start,endAfter),removed=before.slice(start,endBefore);
    result.change={observed:changed,added:added.slice(0,24).map(x=>x.slice(0,240)),removed:removed.slice(0,24).map(x=>x.slice(0,240)),omitted:!!(current.snapshot.omitted||old.snapshot.omitted)||added.length>24||removed.length>24||added.some(x=>x.length>240)||removed.some(x=>x.length>240)};
    const key=JSON.stringify([q.action,q.ref,q.selector,q.frame||[]]);
    if(['click','fill','select','press','scroll','fill_form'].includes(q.action)){
      const count=changed?0:slot.progress?.key===key?slot.progress.count+1:1;
      candidate.progress={key,count};
      result.progress={unchanged_actions:count,note:(count>=2?'Repeated action produced no visible change. Inspect loading, blocking layers or login state; do not assume success or resubmit blindly.':changed?'Visible page state changed; verify the intended outcome.':'Action completed without a visible change; this alone does not establish success or failure.')+(result.change.omitted?' Comparison is bounded; omitted content was not fully compared.':'')};
    }
    // Only elide links when the entire retained ref sequence is identical.
    // Insertions, removals, reorders, frame/range changes require a fresh full
    // bounded view; absence in a delta never means a ref was removed.
    const sameRefs=old.snapshot.elements.length===current.snapshot.elements.length&&current.snapshot.elements.every((e,i)=>e.ref===old.snapshot.elements[i].ref);
    if(q.view==='full'||['open','preview','snapshot'].includes(q.action)||rangeChanged||!sameRefs||added.length>24||removed.length>24)return full();
    result.snapshot.mode=changed?'delta':'unchanged';
    const notes=[];
    if(added.length||removed.length)notes.push(`[Text update: at line ${start+1} of the previous bounded text, replace ${removed.length} lines with ${added.length} lines; other lines unchanged.]`,...added);
    // Use the unabridged splice, never the 240-character change summary.
    // Current non-link controls and changed links remain directly available.
    result.snapshot.elements=current.snapshot.elements.filter((e,i)=>e.tag!=='a'||e.role!=='link'||e.ref===q.ref||JSON.stringify(e)!==JSON.stringify(old.snapshot.elements[i]));
    if(result.snapshot.elements.length<current.snapshot.elements.length)notes.push('[Unlisted links are unchanged; retain their previous refs and order. Current non-link controls and changed links are listed in elements.]');
    result.snapshot.text=notes.join('\n');
    // Text annotations can exceed the text budget when one long line changes.
    // Fall back to the original bounded view, not a partly described delta.
    if(result.snapshot.text.length>12000){result.snapshot=structuredClone(current.snapshot);return full();}
    return bounded();
  };
}
