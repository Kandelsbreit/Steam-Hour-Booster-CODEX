'use strict';
const path=require('node:path');
const {packager}=require('@electron/packager');
(async()=>{
  const dirs=await packager({dir:path.resolve(__dirname,'..'),name:'AgniaSteamHours',platform:'win32',arch:'x64',
    out:path.resolve(__dirname,'../../dist'),overwrite:true,asar:true,prune:true,
    appVersion:require('../package.json').version,buildVersion:require('../package.json').version,
    executableName:'AgniaSteamHours',win32metadata:{CompanyName:'Agnia',FileDescription:'Agnia Steam Hours',ProductName:'Agnia Steam Hours'},
    ignore:[/^\/test(?:\/|$)/,/^\/scripts(?:\/|$)/,/^\/dist(?:\/|$)/,/^\/\.smoke-data(?:\/|$)/]});
  console.log(dirs.join('\n'));
})().catch(e=>{console.error(e);process.exitCode=1;});
