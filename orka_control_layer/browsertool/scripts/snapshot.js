function(q) {
  const slot = globalThis.__orkaBrowserState || (globalThis.__orkaBrowserState = {});
  // Input echoes survive GUI handoff and new runs in the same document.
  // Keep the bounded redaction history until its isolated world is destroyed.
  if (!Array.isArray(slot.redactions)) slot.redactions = [];
  const scope = JSON.stringify(q.scope);
  if (slot.scope !== scope) {
    slot.scope = scope; slot.refs = new Map(); slot.nonce = ''; slot.nodeIDs=new WeakMap(); slot.nextID=0; slot.observation=null; slot.pendingObservation=null; slot.coverageIDs=new WeakMap(); slot.nextCoverageID=0;
  }
  // Existing isolated worlds can outlive an update of the helper scripts.
  if (!slot.coverageIDs) {slot.coverageIDs=new WeakMap();slot.nextCoverageID=0;slot.observation=null;slot.pendingObservation=null;}
  const fail = (code, message) => { throw {orkaCode:code, message}; };
  const clean = (value, limit=160) => {
    let text = String(value || '');
    for (const secret of slot.redactions) if (secret) text = text.split(secret).join('[redacted]');
    text=text.replace(/\s+/g,' ').trim();
    for (const secret of slot.redactions) {
      const normalized=secret.replace(/\s+/g,' ').trim();
      if(normalized)text=text.split(normalized).join('[redacted]');
    }
    return text.slice(0,limit);
  };
  const dom=globalThis.__orkaDOM;
  const {roots,shown,disabled,rawName,fingerprint,interactable}=dom;
  const name=node=>clean(rawName(node));
  const find=()=>dom.find(q,slot,fail);
  const remember = value => {if(value){slot.redactions.push(String(value));slot.redactions=slot.redactions.slice(-32);}};
  const rememberInput = (node, value) => {
    // Public numeric filters and search queries are page data. Masking them
    // corrupts dates, article titles and navigation URLs. Credential-labelled
    // controls still hide echoes, regardless of their input type.
    if(node.tagName==='INPUT' && ['number','search'].includes(node.type)) {
      const purpose=[node.autocomplete,node.id,node.name,node.getAttribute('aria-label'),node.placeholder,node.title,Array.from(node.labels||[]).map(label=>label.textContent).join(' ')].join(' ');
      if(!/password|passcode|secret|token|auth|pin|otp|one-time|cc-|card|account|ssn|密码|口令|验证码|密钥|账号|帐号|卡号|证件/i.test(purpose))return;
    }
    remember(value);
  };
  const snapshot = () => {
    slot.refs=new Map(); slot.nonce=slot.nonce||q.nonce;
    const elements=[],frames=[],texts=[];
    let visited=0,characters=0,omitted=false,pixel_content=false;
    // Internal range metadata contains only node IDs/limits, never page text.
    // Retain a weak identity for roots and cut points, not an unbounded DOM log.
    const coverage={roots:[],textEnd:null,elementLimit:false,frameLimit:false,byteLimit:false};
    const coverageID=node=>{let id=slot.coverageIDs.get(node);if(!id){id=++slot.nextCoverageID;slot.coverageIDs.set(node,id);}return id;};
    const plainLink=node=>node.tagName==='A'&&(!node.getAttribute('role')||node.getAttribute('role')==='link');
    const dropLink=()=>{const i=elements.findLastIndex(e=>e.tag==='a'&&e.role==='link');if(i<0)return false;slot.refs.delete(elements[i].ref);elements.splice(i,1);return true;};
    const selector='a[href],button,input,textarea,select,summary,[role],[tabindex],[contenteditable="true"],iframe,frame,canvas';
    for(const root of roots(document,true)){
      coverage.roots.push(coverageID(root));
      const walker=document.createTreeWalker(root,NodeFilter.SHOW_TEXT);
      let text;
      while(!coverage.textEnd&&(text=walker.nextNode())){
        if(++visited>20000){omitted=true;coverage.textEnd={node:coverageID(text),reason:'visit_limit'};break;}
        const p=text.parentElement;
        if(!p||p.closest('script,style,noscript,textarea,select,[contenteditable="true"]')||!shown(p))continue;
        const value=clean(text.nodeValue,12000-characters);
        if(value){texts.push(value);characters+=value.length+1;}
        if(characters>=12000){omitted=true;coverage.textEnd={node:coverageID(text),reason:'text_limit',length:value.length};break;}
      }
      for(const node of root.querySelectorAll(selector)){
        if(!shown(node))continue;
        if(node.tagName==='CANVAS') {pixel_content=true;continue;}
        if(/^(IFRAME|FRAME)$/.test(node.tagName)) {
          if(frames.length<32)frames.push({title:clean(node.title),url:clean(node.src,1024),path:dom.framePath(node),supported:!!dom.frameDocument(node),reason:dom.frameDocument(node)?undefined:'cross_origin_or_unavailable',handoff:dom.frameDocument(node)?undefined:'gui'});else {omitted=true;coverage.frameLimit=true;}
          continue;
        }
        // Keep the current controls even when hundreds of story links precede
        // them. Evict a trailing plain link, preserving retained DOM order.
        if(elements.length>=200){omitted=true;coverage.elementLimit=true;if(plainLink(node)||!dropLink())continue;}
        let options;
        if(node.tagName==='SELECT'){
          options=[];
          for(const option of node.options){
            if(options.length>=40){omitted=true;break;}
            options.push({label:clean(option.label,160),value:clean(option.value,256),disabled:!!option.disabled||!!option.parentElement.disabled,selected:!!option.selected});
          }
        }
        const identity=fingerprint(node);let savedID=slot.nodeIDs.get(node);
        if(!savedID||savedID.identity!==identity){savedID={ref:'e'+(++slot.nextID),identity};slot.nodeIDs.set(node,savedID);}const ref=savedID.ref;
        slot.refs.set(ref,{node,fingerprint:fingerprint(node),frames:dom.frameChain(node)});
        const role=node.getAttribute('role')||({BUTTON:'button',A:'link',INPUT:node.type==='checkbox'?'checkbox':node.type==='radio'?'radio':'textbox',TEXTAREA:'textbox',SELECT:'combobox',SUMMARY:'button'}[node.tagName]||'');
        elements.push({ref,options,actions:dom.actions(node),frame:dom.documentPath(node.ownerDocument),tag:node.tagName.toLowerCase(),role,name:name(node),type:clean(node.getAttribute('type'),40),disabled:disabled(node),checked:!!node.checked,selected:!!node.selected,editable:!!node.isContentEditable||/^(INPUT|TEXTAREA)$/.test(node.tagName)});
      }
      if(visited>20000)break;
    }
    const result={ok:true,url:clean(location.href,2048),title:clean(document.title,256),snapshot:{id:slot.nonce,ready_state:document.readyState,text:texts.join('\n').slice(0,12000),elements,frames,pixel_content,omitted}};
    // Reserve space for the Go receipt and account for UTF-8/JSON escaping.
    while(new TextEncoder().encode(JSON.stringify(result)).length>60000){
      result.snapshot.omitted=true;
      coverage.byteLimit=true;
      if(elements.length){if(!dropLink()){const removed=elements.pop();slot.refs.delete(removed.ref);}}
      else if(frames.length)frames.pop();
      else result.snapshot.text=result.snapshot.text.slice(0,Math.floor(result.snapshot.text.length/2));
    }
    return globalThis.__orkaObserve(result,q,slot,coverage);
  };
  try {
    // Internal acknowledgement after recovery's stability check. Commands in a
    // lease are serial; scope and document ID must still identify the candidate.
    if(q.operation==='commit_observation'){
      if(!globalThis.__orkaCommitObservation(slot,q.snapshot_id))fail('stale_ref','Observation candidate belongs to an old scope or document.');
      return {ok:true};
    }
    if(q.operation==='snapshot') return snapshot();
    if(q.operation==='wait'){
      const condition=q.condition||(q.ref||q.selector?'visible':'domcontentloaded');
      let ready=false;
      if(condition==='domcontentloaded')ready=document.readyState==='interactive'||document.readyState==='complete';
      else if(condition==='load')ready=document.readyState==='complete';
      else if(condition==='url')ready=location.href===q.url;
      else {
        const node=q.ref||q.selector?find():document.body;
        if(condition==='visible')ready=shown(node);
        else if(condition==='hidden')ready=!shown(node);
        else if(condition==='enabled')ready=shown(node)&&!disabled(node);
        else if(condition==='text')ready=!!node&&shown(node)&&String(node.innerText||node.textContent||'').includes(q.text);
      }
      return {ok:true,ready};
    }
    if(q.operation==='scroll'){
      const node=q.ref||q.selector?find():null;
      if(q.ref||q.selector)interactable(node);
      const distance=q.amount||600,direction=q.direction||'down';
      (node||window).scrollBy({left:direction==='left'?-distance:direction==='right'?distance:0,top:direction==='up'?-distance:direction==='down'?distance:0,behavior:'instant'});
      return {ok:true};
    }
    const node=find();
    const point=interactable(node);
    if(q.operation==='prepare')return {ok:true};
    if(q.operation==='click'){
      if(node.closest('a[target="_blank"],form[target="_blank"]')||node.getAttribute('formtarget')==='_blank')fail('unsupported_popup','Popup interaction is not supported; use the visual GUI.');
      return {ok:true,...point};
    }
    if(q.operation==='press'){node.focus();return {ok:true};}
    if(q.operation==='fill'){
      if(node.readOnly)fail('not_interactable','Target is read-only.');
      rememberInput(node,q.text);
      node.focus();
      if(node.isContentEditable)node.textContent=q.text;
      else {
        if(!/^(INPUT|TEXTAREA)$/.test(node.tagName)||/^(file|checkbox|radio|button|submit|reset|image|hidden)$/.test(node.type))fail('not_interactable','Target is not a text input.');
        const view=node.ownerDocument.defaultView;
        const prototype=node.tagName==='TEXTAREA'?view.HTMLTextAreaElement.prototype:view.HTMLInputElement.prototype;
        Object.getOwnPropertyDescriptor(prototype,'value').set.call(node,q.text);
      }
      node.dispatchEvent(new InputEvent('input',{bubbles:true,composed:true,inputType:'insertText',data:q.text}));
      node.dispatchEvent(new Event('change',{bubbles:true,composed:true}));
      return {ok:true};
    }
    if(q.operation==='select'){
      if(node.tagName!=='SELECT')fail('not_interactable','Target is not a select element.');
      const option=Array.from(node.options).find(option=>option.value===q.value);
      if(!option||option.disabled||option.parentElement.disabled)fail('not_interactable','Requested option is missing or disabled.');
      remember(q.value);
      Object.getOwnPropertyDescriptor(node.ownerDocument.defaultView.HTMLSelectElement.prototype,'value').set.call(node,q.value);
      node.dispatchEvent(new Event('input',{bubbles:true,composed:true}));
      node.dispatchEvent(new Event('change',{bubbles:true,composed:true}));
      return {ok:true};
    }
    fail('invalid_request','Unsupported browser operation.');
  } catch(error) {
    return {ok:false,error:{code:error?.orkaCode||'script_error',message:error?.orkaCode?error.message:'Browser page script failed.'}};
  }
}
