'use strict';
const test=require('node:test'),assert=require('node:assert/strict');
const baseline=require('../../docs/engine-1.0.0');
const current=require('../src/engine');
test('every original engine method except conflict failure handler is unchanged',()=>{
  for(const name of Object.getOwnPropertyNames(baseline.Engine.prototype)){
    if(['constructor','failure'].includes(name))continue;
    assert.equal(current.Engine.prototype[name].toString(),baseline.Engine.prototype[name].toString(),name+' was modified');
  }
  for(const name of ['parseIds','settings'])assert.equal(current[name].toString(),baseline[name].toString(),name);
});
