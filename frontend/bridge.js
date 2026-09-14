'use strict';
window.steamHours = Object.freeze({
 command: (name, payload) => window.go.main.App.Command(name, payload || {}),
 subscribe: callback => window.runtime.EventsOn('snapshot', callback),
 onError: callback => window.runtime.EventsOn('app-error', callback)
});
