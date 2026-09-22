import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import ts from "typescript";
import { QueryClient } from "@tanstack/react-query";

function setup() {
  const requests = [],
    listeners = new Map(),
    notices = [];
  let sequence = 0;
  const folders = new Map(), jobs = [], calls = [];
  const managed = async (view, params, destination) => {
    calls.push({ view, params, destination });
    if (view === "files") return { entries: folders.get(destination) ?? [] };
    if (view === "plan") return { fingerprint: "fresh", confirmation: params.params.target };
    if (view === "run") { const job = { id: params.id, status: "succeeded", result: {} }; jobs.push(job); return job; }
    if (view === "jobs") return jobs;
    throw Error("Unexpected request " + view);
  };
  class XHR {
    upload = {};
    open(method, url) {
      this.method = method;
      this.url = url;
    }
    setRequestHeader() {}
    send(file) {
      this.file = file;
      requests.push(this);
    }
    abort() {
      this.aborted = true;
      this.onabort();
    }
    finish(status = 204) {
      this.status = status;
      this.onload();
    }
  }
  const module = { exports: {} };
  const code = ts.transpileModule(
    readFileSync(new URL("../frontend/uploads.ts", import.meta.url), "utf8"),
    {
      compilerOptions: {
        target: ts.ScriptTarget.ES2022,
        module: ts.ModuleKind.CommonJS,
      },
    },
  ).outputText;
  vm.runInNewContext(code, {
    module,
    exports: module.exports,
    XMLHttpRequest: XHR, URLSearchParams, TextEncoder, setTimeout, clearTimeout,
    require: (name) =>
      name === "@panasms/layout"
        ? { newID: () => `upload-${++sequence}` }
        : name === "@panasms/operations" ? { managed } : { tr: (key) => key },
    window: {
      addEventListener: (name, fn) => {
        const set = listeners.get(name) ?? new Set();
        set.add(fn);
        listeners.set(name, set);
      },
      removeEventListener: (name, fn) => listeners.get(name)?.delete(fn),
      dispatchEvent: (event) => notices.push(event),
    },
    CustomEvent: class {
      constructor(type, options) {
        this.type = type;
        this.detail = options.detail;
      }
    },
  });
  const q = new QueryClient();
  const flush = async () => {
    for (let i = 0; i < 60; i++) await Promise.resolve();
  };
  return {
    q, folders, calls, startFiles: module.exports.enqueueFiles,
    requests,
    listeners,
    notices,
    flush,
    start: module.exports.enqueueUpload,
    tasks: () => q.getQueryData(["file-uploads"]),
  };
}

test("serial batches survive without a mounted page and preserve destinations, failures and aggregate progress", async () => {
  const s = setup();
  s.start(
    s.q,
    [
      { name: "first.txt", size: 100 },
      { name: "second.txt", size: 300 },
    ],
    "/home/a",
  );
  s.start(s.q, [{ name: "third.txt", size: 0 }], "/home/b");
  await s.flush();
  assert.equal(s.requests.length, 1);
  s.requests[0].upload.onprogress({ lengthComputable: true, loaded: 50 });
  assert.equal(s.tasks()[0].loaded, 50);
  s.requests[0].finish();
  await s.flush();
  assert.equal(s.tasks()[0].completed, 1);
  s.requests[1].upload.onprogress({ lengthComputable: true, loaded: 150 });
  assert.equal(s.tasks()[0].loaded, 250);
  s.requests[1].finish(409);
  await s.flush();
  assert.equal(s.tasks()[0].status, "failed");
  assert.match(s.tasks()[0].errors[0], /second.txt/);
  assert.match(s.requests[2].url, /%2Fhome%2Fb%2Fthird.txt$/);
  s.requests[2].finish();
  await s.flush();
  assert.equal(s.tasks()[1].status, "succeeded");
  assert.equal(s.listeners.get("beforeunload").size, 0);
  s.q.clear();
});

test("cancel aborts current request and skips remaining files while allowing next batch", async () => {
  const s = setup();
  s.start(
    s.q,
    [
      { name: "one", size: 100 },
      { name: "two", size: 100 },
    ],
    "/a",
  );
  s.start(s.q, [{ name: "three", size: 100 }], "/b");
  await s.flush();
  s.tasks()[0].cancel();
  await s.flush();
  assert.equal(s.requests[0].aborted, true);
  assert.equal(s.tasks()[0].status, "cancelled");
  assert.equal(s.requests.length, 2);
  assert.equal(s.requests[1].file.name, "three");
  s.requests[1].finish();
  await s.flush();
  s.q.clear();
});

test("session clear aborts uploads and prevents queued requests from running as another user", async () => {
  const s = setup();
  s.start(s.q, [{ name: "one", size: 100 }], "/a");
  s.start(s.q, [{ name: "two", size: 100 }], "/b");
  await s.flush();
  s.q.clear();
  await s.flush();
  assert.equal(s.requests[0].aborted, true);
  assert.equal(s.requests.length, 1);
  assert.equal(s.tasks(), undefined);
  assert.equal(s.listeners.get("beforeunload").size, 0);
});


test("conflict waits without starting transfer; rename keeps the original target", async () => {
 const s = setup();
 s.folders.set('/a', [{ name: 'one.txt', path: '/a/one.txt', directory: false, revision: 'existing' }]);
 s.start(s.q, [{ name: 'one.txt', size: 5 }], '/a'); await s.flush();
 assert.equal(s.requests.length, 0); assert.equal(s.tasks()[0].status, 'waiting');
 s.tasks()[0].conflict.resolve({ mode: 'rename', name: 'one-new.txt' }); await s.flush();
 assert.match(s.requests[0].url, /one-new.txt/); assert.doesNotMatch(s.requests[0].url, /replace_revision/);
 s.requests[0].finish(); await s.flush(); assert.equal(s.tasks()[0].status, 'succeeded'); s.q.clear();
});
test("skip remaining conflicts and cancel a waiting batch issue no writes", async () => {
 const s = setup();
 s.folders.set('/a', ['one','two'].map(name => ({ name, path: '/a/'+name, directory: false, revision: name })));
 s.start(s.q, [{name:'one',size:5},{name:'two',size:5}], '/a'); await s.flush();
 s.tasks()[0].conflict.resolve({ mode:'skip', all:true }); await s.flush();
 assert.equal(s.tasks()[0].skipped, 2); assert.equal(s.requests.length, 0);
 s.start(s.q, [{name:'one',size:5}], '/a'); await s.flush(); s.tasks().at(-1).cancel(); await s.flush();
 assert.equal(s.tasks().at(-1).status, 'cancelled'); assert.equal(s.requests.length,0); s.q.clear();
});
test("copy and move selected items in background with exact target plans and replacement revisions", async () => {
 const s = setup();
 s.folders.set('/dest', [{name:'one',path:'/dest/one',directory:false,revision:'existing'}]);
 s.startFiles(s.q, 'copy', [{name:'one',path:'/source/one',directory:false},{name:'folder',path:'/source/folder',directory:true}], '/dest');
 await s.flush(); s.tasks()[0].conflict.resolve({mode:'replace'}); await s.flush();
 const runs=s.calls.filter(call=>call.view==='run');
 assert.equal(runs.length,2); assert.equal(runs[0].params.params.replace_revision,'existing');
 assert.equal(runs[1].params.params.destination,'/dest/folder'); assert.equal(s.tasks()[0].completed,2);
 s.startFiles(s.q,'move',[{name:'three',path:'/source/three',directory:false}],'/dest');await s.flush();
 assert.equal(s.calls.filter(call=>call.view==='run').at(-1).params.action,'file.move'); s.q.clear();
});
