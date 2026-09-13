'use strict';
const { contextBridge, ipcRenderer } = require('electron');
contextBridge.exposeInMainWorld('steamHours', {
  command: (command, payload) => ipcRenderer.invoke('command', command, payload),
  onError: callback => ipcRenderer.on('app-error', (_event, message) => callback(message)),
  subscribe: callback => {
    const listener = (_event, state) => callback(state);
    ipcRenderer.on('snapshot', listener);
    return () => ipcRenderer.removeListener('snapshot', listener);
  }
});
