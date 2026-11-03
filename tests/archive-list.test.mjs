import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import ts from "typescript";

function load(name, imports, globals = {}) {
  const source = fs.readFileSync(new URL("../frontend/" + name + ".ts", import.meta.url), "utf8");
  const code = ts.transpileModule(source, {compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS,esModuleInterop:true}}).outputText;
  const exports = {};
  vm.runInNewContext(code, {exports, require: key => imports[key], URLSearchParams, ...globals});
  return exports;
}
test("cloud and nested cloud archive folders use cancellable content requests", async () => {
  const { folderTarget, archiveSource } = load("archive-folder", {});
  const calls = [];
  const { listFolder } = load("list-folder", {
    "@panasms/operations":{managed:(...args)=>calls.push(args)},
    "./archive-folder":{archiveSource}, "./i18n":{tr:key=>key}, "./server-messages.json":[],
  }, {fetch:async (url,options)=>{calls.push([url,options]);return {ok:true,json:async()=>({entries:[],readOnly:true})};}});
  const root = folderTarget({name:"sample.zip",path:"cloud:"+ "a".repeat(32)+"/sample.zip"});
  const nested=folderTarget({name:"inner.zip",path:root+"/inner.zip"});
  const signal=new AbortController().signal;
  for(const target of [root,root+"/pictures",nested]){
    const result=await listFolder(target,signal);
    assert.equal(result.readOnly,true);
    const [url,options]=calls.pop();
    assert.equal(new URL(url,"http://host").searchParams.get("target"),"archive-list:"+target);
    assert.equal(options.signal,signal);
  }
  for(const path of ["/home/user","cloud:"+"a".repeat(32),folderTarget({name:"local.zip",path:"/home/user/local.zip"})]){
    await listFolder(path);
    assert.deepEqual(calls.pop(),["files",undefined,path]);
  }
});
