'use strict';
'require view';
'require sota.routingview as routing';

return view.extend({
  handleSave:null, handleSaveApply:null, handleReset:null,
  render() { return routing.renderPage('lists', 'Списки маршрутизации'); }
});

