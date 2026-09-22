function(input) {
  const snapshot=structuredClone(input.snapshot);
  const clean=(value,limit=160)=>String(value||'').replace(/\s+/g,' ').trim().slice(0,limit);
  const shown=node=>{
    if(node.hidden||node.inert||node.getAttribute?.('aria-hidden')==='true')return false;
    try{if(typeof node.getClientRects==='function'&&node.getClientRects().length===0)return false;}catch{return false;}
    return true;
  };
  const rawName=node=>{
    const ids=(node.getAttribute?.('aria-labelledby')||'').split(/\s+/).filter(Boolean);
    return node.getAttribute?.('aria-label')||ids.map(id=>node.getRootNode?.().getElementById?.(id)?.textContent||'').join(' ').trim()||Array.from(node.labels||[]).map(label=>label.textContent).join(' ')||node.getAttribute?.('alt')||node.title||node.textContent||'';
  };
  const roots=[document];
  for(let i=0;i<roots.length&&i<200;i++)for(const node of roots[i].querySelectorAll?.('*')||[]){
    if(roots.length>=200)break;
    if(node.shadowRoot)roots.push(node.shadowRoot);
    if(/^(IFRAME|FRAME)$/.test(node.tagName)){try{if(node.contentDocument?.documentElement)roots.push(node.contentDocument);}catch{}}
  }
  const targets=[];
  const targetLimit=Math.min(512,Math.max(32,snapshot.elements.length*4));
  let targetBytes=0;
  for(const root of roots)for(const node of root.querySelectorAll?.('a[href]')||[]){
    if(targets.length>=targetLimit||!shown(node))continue;
    try{
      const target=new URL(node.getAttribute('href'),location.href);
      if(!/^https?:$/.test(target.protocol)||target.username||target.password||target.href.length>2048)continue;
      const item={name:clean(rawName(node)),href:target.href};
      targetBytes+=item.name.length+item.href.length;
      if(targetBytes>48000)break;
      targets.push(item);
    }catch{}
  }
  const queues=new Map();
  for(const target of targets){const queue=queues.get(target.name)||[];queue.push(target.href);queues.set(target.name,queue);}
  const interactiveNames=new Set();
  for(const element of snapshot.elements){
    if(element.tag==='a'&&element.role==='link'){
      const queue=queues.get(element.name||'');
      if(queue?.length){element.href=queue.shift();element.actions=undefined;}
    }
    if(element.name&&['a','button','summary'].includes(element.tag))interactiveNames.add(element.name);
  }
  if(snapshot.mode==='full'){
    const compact=[];let previous='';
    for(const line of snapshot.text.split('\n')){
      const value=clean(line,Number.MAX_SAFE_INTEGER);
      if(!value||value===previous||interactiveNames.has(value))continue;
      compact.push(value);previous=value;
    }
    snapshot.text=compact.join('\n');
  }else if(snapshot.mode==='delta'){
    snapshot.text=snapshot.text.replace(/^\[Text update:[^\n]*\]\n?/, '[Changed text follows; other previously observed content is unchanged.]\n');
  }
  const encoder=new TextEncoder(),budget=Math.max(4096,Number(input.byte_limit)||8192);
  const size=()=>encoder.encode(JSON.stringify(snapshot)).length;
  while(size()>budget){
    const index=snapshot.elements.findLastIndex(element=>element.tag==='a'&&element.role==='link');
    if(index>=0)snapshot.elements.splice(index,1);
    else if(snapshot.text.length>256)snapshot.text=snapshot.text.slice(0,Math.floor(snapshot.text.length*.8));
    else break;
    snapshot.omitted=true;
  }
  return snapshot;
}
