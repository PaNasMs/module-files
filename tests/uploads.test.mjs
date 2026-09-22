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
    XMLHttpRequest: XHR,
    require: (name) =>
      name === "@panasms/layout"
        ? { newID: () => `upload-${++sequence}` }
        : { tr: (key) => key },
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
    for (let i = 0; i < 15; i++) await Promise.resolve();
  };
  return {
    q,
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
