async function(source) {
  let value;
  try {
    value = await (0,eval)(source);
    if (value === undefined) value = null;
    const json = JSON.stringify(value);
    if (typeof json !== 'string') throw new Error();
    if (new TextEncoder().encode(json).length > 60000) {
      return {ok:false,error:{code:'output_limit',message:'JavaScript result exceeds the output limit.'}};
    }
    return {ok:true,value:JSON.parse(json)};
  } catch (_) {
    return {ok:false,error:{code:'script_error',message:'Browser page script failed.'}};
  }
}
