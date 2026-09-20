function() {
  if(globalThis.__orkaDOM)return;
  const fail=(code,message)=>{throw {orkaCode:code,message};};
  const frameDocument=node=>{try{return node.contentDocument?.documentElement?node.contentDocument:null;}catch{return null;}};
  const roots=(doc=document,frames=false)=>{
    const found=[doc];
    for(let i=0;i<found.length&&i<200;i++){
      for(const node of found[i].querySelectorAll('*')){
        if(found.length>=200)break;
        if(node.shadowRoot)found.push(node.shadowRoot);
        if(frames&&/^(IFRAME|FRAME)$/.test(node.tagName)){
          const child=frameDocument(node);if(child)found.push(child);
        }
      }
    }
    return found;
  };
  const parent=node=>node.parentElement||node.getRootNode().host||node.ownerDocument.defaultView?.frameElement;
  const shown=node=>{
    if(!node?.isConnected)return false;
    for(let n=node;n;n=parent(n)){
      const s=n.ownerDocument.defaultView.getComputedStyle(n);
      if(s.display==='none'||s.visibility==='hidden'||s.visibility==='collapse'||n.hidden||n.inert||n.getAttribute('aria-hidden')==='true')return false;
    }
    return node.getClientRects().length>0;
  };
  const disabled=node=>!!node.disabled||node.getAttribute('aria-disabled')==='true'||!!node.closest('fieldset[disabled]');
  const rawName=node=>{
    const ids=(node.getAttribute('aria-labelledby')||'').split(/\s+/).filter(Boolean);
    return node.getAttribute('aria-label')||ids.map(id=>node.getRootNode().getElementById?.(id)?.textContent||'').join(' ').trim()||Array.from(node.labels||[]).map(n=>n.textContent).join(' ')||node.getAttribute('alt')||node.title||(!node.isContentEditable&&!/^(INPUT|TEXTAREA|SELECT)$/.test(node.tagName)?node.textContent:'')||node.getAttribute('placeholder')||'';
  };
  // Use unredacted semantic attributes internally: adding input redactions must
  // not alter an unrelated element's identity. These values never leave here.
  const fingerprint=node=>JSON.stringify([node.tagName,node.id,node.getAttribute('name'),node.getAttribute('type'),node.getAttribute('role'),node.getAttribute('aria-label'),node.getAttribute('href'),node.getAttribute('formaction'),rawName(node)]);
  const frameChain=node=>{
    const chain=[];let doc=node.ownerDocument;
    while(doc!==document){const frame=doc.defaultView?.frameElement;if(!frame)break;chain.unshift(frame);doc=frame.ownerDocument;}
    return chain;
  };
  const selectorFor=node=>{
    if(node.id&&node.getRootNode().querySelectorAll('#'+CSS.escape(node.id)).length===1)return '#'+CSS.escape(node.id);
    const parts=[];for(let n=node;n?.nodeType===1;n=n.parentElement){
      const peers=Array.from(n.parentElement?.children||[]).filter(p=>p.tagName===n.tagName);
      parts.unshift(n.tagName.toLowerCase()+':nth-of-type('+(peers.indexOf(n)+1)+')');
    }
    return parts.join(' > ');
  };
  const framePath=node=>[...frameChain(node),node].map(selectorFor);
  const documentPath=doc=>doc===document?[]:frameChain(doc.documentElement).map(selectorFor);
  const unique=(doc,selector)=>{
    const matches=[];
    try{for(const root of roots(doc)){for(const node of root.querySelectorAll(selector)){matches.push(node);if(matches.length>1)break;}if(matches.length>1)break;}}
    catch{fail('invalid_request','Invalid CSS selector.');}
    if(matches.length>1)fail('ambiguous_selector','CSS selector matches multiple elements; use snapshot refs or a more specific selector.');
    return matches[0]||null;
  };
  const find=(q,slot)=>{
    if(q.ref){
      if(!q.snapshot_id||q.snapshot_id!==slot.nonce)fail('stale_ref','Reference belongs to an old document, run or GUI epoch; observe again.');
      const saved=slot.refs.get(q.ref);
      const chain=saved?frameChain(saved.node):[];
      if(!saved||!saved.node.isConnected||saved.fingerprint!==fingerprint(saved.node)||chain.length!==saved.frames.length||chain.some((f,i)=>f!==saved.frames[i]||!f.isConnected))fail('stale_ref','Element was replaced or changed; observe again instead of replaying an old target.');
      let doc=saved.node.ownerDocument;
      for(let i=chain.length-1;i>=0;i--){if(chain[i].contentDocument!==doc)fail('stale_ref','Frame navigated; observe again.');doc=chain[i].ownerDocument;}
      if(doc!==document)fail('stale_ref','Element document is detached; observe again.');
      return saved.node;
    }
    let doc=document;
    for(const selector of q.frame||[]){
      const frame=unique(doc,selector);
      if(!frame)fail('not_ready','Frame is not present yet.');
      if(!/^(IFRAME|FRAME)$/.test(frame.tagName))fail('invalid_request','Frame path must identify frame elements.');
      if(!shown(frame))fail('not_ready','Frame is hidden.');
      doc=frameDocument(frame);
      if(!doc)fail('unsupported_frame','Frame DOM is cross-origin or unavailable; use GUI on the same page.');
    }
    return unique(doc,q.selector);
  };
  const interactable=node=>{
    if(!node)fail('not_ready','Target element is not present yet.');
    if(/^(IFRAME|FRAME)$/.test(node.tagName))fail('unsupported_frame','Select an element inside a supported frame, or use GUI.');
    if(!shown(node)||disabled(node))fail('not_ready','Target is hidden, inert or disabled.');
    node.scrollIntoView({block:'center',inline:'center',behavior:'instant'});
    let doc=node.ownerDocument;const view=doc.defaultView,r=node.getBoundingClientRect();
    let x=(Math.max(0,r.left)+Math.min(view.innerWidth,r.right))/2,y=(Math.max(0,r.top)+Math.min(view.innerHeight,r.bottom))/2;
    const hitAt=(document,x,y)=>{let hit=document.elementFromPoint(x,y);while(hit?.shadowRoot){const next=hit.shadowRoot.elementFromPoint(x,y);if(!next||next===hit)break;hit=next;}return hit;};
    let hit=hitAt(doc,x,y);
    if(r.width<=0||r.height<=0||!hit||!(hit===node||node.contains(hit)))fail('not_ready','Target has no uncovered interaction point.');
    while(doc!==document){
      const frame=doc.defaultView.frameElement;
      // Avoid guessing transformed iframe coordinates. GUI can handle these.
      for(let n=frame;n;n=parent(n)){if(n.ownerDocument.defaultView.getComputedStyle(n).transform!=='none')fail('unsupported_frame','Transformed frame requires GUI.');}
      const rect=frame.getBoundingClientRect();x+=rect.left+frame.clientLeft;y+=rect.top+frame.clientTop;doc=frame.ownerDocument;
      if(hitAt(doc,x,y)!==frame)fail('not_ready','Frame is clipped or covered.');
    }
    return {x,y};
  };
  const actions=node=>{
    if(disabled(node))return [];
    if(node.tagName==='SELECT')return ['select'];
    if(node.isContentEditable||node.tagName==='TEXTAREA'||node.tagName==='INPUT'&&!/^(file|checkbox|radio|button|submit|reset|image|hidden)$/.test(node.type))return node.readOnly?['press']:['fill','press'];
    return ['click','press'];
  };
  globalThis.__orkaDOM={roots,shown,disabled,rawName,fingerprint,frameDocument,framePath,frameChain,documentPath,find,interactable,actions};
}
