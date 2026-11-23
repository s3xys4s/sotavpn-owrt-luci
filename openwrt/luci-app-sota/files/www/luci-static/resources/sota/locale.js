'use strict';
'require baseclass';

return baseclass.extend({
  resource: function(name) {
    return L.resource(name) + '?v=' + encodeURIComponent(L.env.resource_version);
  },
  load: async function() {
    const module = await import(this.resource('sota/i18n.js'));
    return module.loadLocale(document.documentElement.lang || 'en');
  }
});
