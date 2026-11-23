'use strict';
'require baseclass';
'require rpc';
'require sota.locale as language';
'require sota.toolbar as toolbar';

const readPart = rpc.declare({object:'sota',method:'readpart',params:['revision','offset'],reject:true});
const writePart = rpc.declare({object:'sota',method:'writepart',params:['transfer','offset','chunk','final'],reject:true});
async function unwrap(promise) {
  const reply = await promise;
  if (reply.error) throw Error(reply.error);
  return reply.data;
}
async function read() {
  let bytes, offset=0, revision='';
  do {
    const result=await unwrap(readPart(revision,offset));
    if (!bytes) bytes=new Uint8Array(result.size);
    revision=result.revision;
    const piece=Uint8Array.from(atob(result.chunk),c=>c.charCodeAt(0));
    if (!piece.length) throw Error('Неполный ответ маршрутизатора');
    bytes.set(piece,offset);offset+=piece.length;
  } while(offset<bytes.length);
  return JSON.parse(new TextDecoder().decode(bytes));
}
return baseclass.extend({
  async renderPage(page, title) {
    const locale = await language.load();
    const module = await import(language.resource('sota-routing.js'));
    const apply = async value => {
      await module.writeRoutingChunks(value, (...args) => unwrap(writePart(...args)));
      return read();
    };
    const form = await module.routingEditor(read,apply,locale,page);
    return E('div',{},[E('link',{rel:'stylesheet',href:language.resource('sota/routes.css')}),E('h2',{},locale.t(title)),toolbar.savedSelection(locale,()=>form.refreshIfClean?.()),form]);
  }
});

