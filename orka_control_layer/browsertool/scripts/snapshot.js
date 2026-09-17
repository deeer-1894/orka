function(q) {
  const slot = globalThis.__orkaBrowserState || (globalThis.__orkaBrowserState = {});
  // Input echoes survive GUI handoff and new runs in the same document.
  // Keep the bounded redaction history until its isolated world is destroyed.
  if (!Array.isArray(slot.redactions)) slot.redactions = [];
  const scope = JSON.stringify(q.scope);
  if (slot.scope !== scope) {
    slot.scope = scope; slot.refs = new Map(); slot.nonce = '';
  }
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
  const roots = () => {
    const found = [document];
    for (let i=0; i<found.length && i<200; i++) {
      for (const node of found[i].querySelectorAll('*')) {
        if(found.length>=200)break;
        if(node.shadowRoot)found.push(node.shadowRoot);
      }
    }
    return found.slice(0,200);
  };
  const parent = node => node.parentElement || (node.getRootNode() instanceof ShadowRoot ? node.getRootNode().host : null);
  const shown = node => {
    if (!node || !node.isConnected) return false;
    for (let n=node; n; n=parent(n)) {
      const s=getComputedStyle(n);
      if (s.display==='none' || s.visibility==='hidden' || s.visibility==='collapse' || n.hidden || n.inert || n.getAttribute('aria-hidden')==='true') return false;
    }
    return node.getClientRects().length > 0;
  };
  const disabled = node => !!node.disabled || node.getAttribute('aria-disabled')==='true' || !!node.closest('fieldset[disabled]');
  const name = node => {
    const ids=(node.getAttribute('aria-labelledby')||'').split(/\s+/).filter(Boolean);
    const labelled=ids.map(id=>node.getRootNode().getElementById?.(id)?.textContent||'').join(' ').trim();
    return clean(node.getAttribute('aria-label') || labelled || Array.from(node.labels||[]).map(n=>n.textContent).join(' ') || node.getAttribute('alt') || node.getAttribute('title') || (!node.isContentEditable&&!/^(INPUT|TEXTAREA|SELECT)$/.test(node.tagName)?node.textContent:'') || node.getAttribute('placeholder'));
  };
  const fingerprint = node => JSON.stringify([node.tagName,node.id,node.getAttribute('name'),node.getAttribute('type'),node.getAttribute('role'),node.getAttribute('aria-label'),node.getAttribute('href'),name(node)]);
  const find = () => {
    if (q.ref) {
      if (!q.snapshot_id || q.snapshot_id!==slot.nonce) fail('stale_ref','Snapshot reference no longer belongs to this page/run epoch.');
      const saved=slot.refs.get(q.ref);
      if (!saved || !saved.node.isConnected || saved.fingerprint!==fingerprint(saved.node)) fail('stale_ref','Referenced element was removed, replaced or changed.');
      return saved.node;
    }
    let matches=[];
    try {for (const root of roots()) {for(const node of root.querySelectorAll(q.selector)){matches.push(node);if(matches.length>1)break;} if(matches.length>1)break;}}
    catch (_) {fail('invalid_request','Invalid CSS selector.');}
    if (matches.length>1) fail('ambiguous_selector','CSS selector must identify exactly one element.');
    return matches[0]||null;
  };
  const interactable = (node, scroll=true) => {
    if (!node) fail('not_interactable','Target element was not found.');
    if (/^(IFRAME|FRAME)$/.test(node.tagName)) fail('unsupported_frame','Frame interaction is not supported; use the visual GUI.');
    if (!shown(node)||disabled(node)) fail('not_interactable','Target is hidden, inert or disabled.');
    if (scroll) node.scrollIntoView({block:'center',inline:'center',behavior:'instant'});
    const r=node.getBoundingClientRect();
    const x=(Math.max(0,r.left)+Math.min(innerWidth,r.right))/2, y=(Math.max(0,r.top)+Math.min(innerHeight,r.bottom))/2;
    if(r.width<=0||r.height<=0||x<0||y<0||x>=innerWidth||y>=innerHeight)fail('not_interactable','Target has no visible interaction point.');
    let hit=document.elementFromPoint(x,y);
    while(hit?.shadowRoot){const next=hit.shadowRoot.elementFromPoint(x,y);if(!next||next===hit)break;hit=next;}
    if(!hit || !(hit===node||node.contains(hit))) fail('not_interactable','Target is covered by another element.');
    return {x,y};
  };
  const remember = value => {if(value){slot.redactions.push(String(value));slot.redactions=slot.redactions.slice(-32);}};
  const rememberInput = (node, value) => {
    // Numeric filters are public page data; masking "5" globally corrupts dates
    // and statistics. Credential-labelled numeric controls still hide echoes.
    if(node.tagName==='INPUT' && node.type==='number') {
      const purpose=[node.autocomplete,node.id,node.name,node.getAttribute('aria-label'),node.placeholder,node.title,Array.from(node.labels||[]).map(label=>label.textContent).join(' ')].join(' ');
      if(!/password|passcode|secret|token|auth|pin|otp|one-time|cc-|card|account|ssn|密码|口令|验证码|密钥|账号|帐号|卡号|证件/i.test(purpose))return;
    }
    remember(value);
  };
  const snapshot = () => {
    slot.refs=new Map(); slot.nonce=q.nonce;
    const elements=[],frames=[],texts=[];
    let visited=0,characters=0,omitted=false,pixel_content=false;
    const selector='a[href],button,input,textarea,select,summary,[role],[tabindex],[contenteditable="true"],iframe,frame,canvas';
    for(const root of roots()){
      const walker=document.createTreeWalker(root,NodeFilter.SHOW_TEXT);
      let text;
      while((text=walker.nextNode())){
        if(++visited>20000){omitted=true;break;}
        const p=text.parentElement;
        if(!p||p.closest('script,style,noscript,textarea,select,[contenteditable="true"]')||!shown(p))continue;
        const value=clean(text.nodeValue,12000-characters);
        if(value){texts.push(value);characters+=value.length+1;}
        if(characters>=12000){omitted=true;break;}
      }
      for(const node of root.querySelectorAll(selector)){
        if(!shown(node))continue;
        if(node.tagName==='CANVAS') {pixel_content=true;continue;}
        if(/^(IFRAME|FRAME)$/.test(node.tagName)) {
          if(frames.length<32)frames.push({title:clean(node.title),url:clean(node.src,1024),supported:false});else omitted=true;
          continue;
        }
        if(elements.length>=200){omitted=true;continue;}
        let options;
        if(node.tagName==='SELECT'){
          options=[];
          for(const option of node.options){
            if(options.length>=40){omitted=true;break;}
            options.push({label:clean(option.label,160),value:clean(option.value,256),disabled:!!option.disabled||!!option.parentElement.disabled,selected:!!option.selected});
          }
        }
        const ref='e'+(elements.length+1);
        slot.refs.set(ref,{node,fingerprint:fingerprint(node)});
        const role=node.getAttribute('role')||({BUTTON:'button',A:'link',INPUT:node.type==='checkbox'?'checkbox':node.type==='radio'?'radio':'textbox',TEXTAREA:'textbox',SELECT:'combobox',SUMMARY:'button'}[node.tagName]||'');
        elements.push({ref,options,tag:node.tagName.toLowerCase(),role,name:name(node),type:clean(node.getAttribute('type'),40),disabled:disabled(node),checked:!!node.checked,selected:!!node.selected,editable:!!node.isContentEditable||/^(INPUT|TEXTAREA)$/.test(node.tagName)});
      }
      if(visited>20000)break;
    }
    const result={ok:true,url:clean(location.href,2048),title:clean(document.title,256),snapshot:{id:slot.nonce,text:texts.join('\n').slice(0,12000),elements,frames,pixel_content,omitted}};
    // Reserve space for the Go receipt and account for UTF-8/JSON escaping.
    while(new TextEncoder().encode(JSON.stringify(result)).length>60000){
      result.snapshot.omitted=true;
      if(elements.length){const removed=elements.pop();slot.refs.delete(removed.ref);}
      else if(frames.length)frames.pop();
      else result.snapshot.text=result.snapshot.text.slice(0,Math.floor(result.snapshot.text.length/2));
    }
    return result;
  };
  try {
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
        const prototype=node.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype;
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
      Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set.call(node,q.value);
      node.dispatchEvent(new Event('input',{bubbles:true,composed:true}));
      node.dispatchEvent(new Event('change',{bubbles:true,composed:true}));
      return {ok:true};
    }
    fail('invalid_request','Unsupported browser operation.');
  } catch(error) {
    return {ok:false,error:{code:error?.orkaCode||'script_error',message:error?.orkaCode?error.message:'Browser page script failed.'}};
  }
}
