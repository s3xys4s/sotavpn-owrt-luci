'use strict';
'require baseclass';
'require rpc';
'require ui';
'require poll';

const actions = [
  ['connect','Старт','sota-start'], ['disconnect','Стоп','sota-stop'], ['restart','Перезапуск','sota-restart']
];
const call = {};
for (const [name] of actions) {
  call[name] = rpc.declare({object:'sota',method:name,params:name==='disconnect'?[]:['id','protocol'],reject:true});
}
const status = rpc.declare({object:'sota',method:'status',reject:true});
return baseclass.extend({
  row(t, buttons) {
    buttons.forEach((button,i)=>button.classList.add(actions[i][2]));
    return E('div',{'class':'sota-service-actions',role:'group','aria-label':t('Управление туннелем')},buttons);
  },
  savedSelection(locale, after) {
    let busy=false;
    const t=msg=>locale.t(msg);
    const buttons=actions.map(([name,title])=>E('button',{type:'button','class':'cbi-button',click:async()=>{
      if(busy)return;
      busy=true; buttons.forEach(b=>b.disabled=true);
      try {
        const reply=await (name==='disconnect'?call[name]():call[name](0,''));
        if(reply.error)throw Error(reply.error);
        await after();
      } catch(e) {ui.addNotification(null,E('p',{},t(e.message)),'error')}
      finally {busy=false;await refresh()}
    }},t(title)));
    async function refresh() {
      let s;
      try {s=(await status()).data} catch {}
      if(busy)return;
      buttons[0].disabled=s?.core?.core_state===2;
      buttons[1].disabled=!(s?.want_tunnel||s?.core?.policy_enabled||s?.core?.core_state>0||s?.provider_session);
      buttons[2].disabled=false;
    }
    poll.add(refresh,5); refresh();
    return this.row(t,buttons);
  }
});

